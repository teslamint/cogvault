// Package trial is the no-backup public-PDF trial of the collection queue
// (ADR 0028). It is a separate, narrower way to run the HTTP collector, and it
// is deliberately not the production path: it never calls the ADR 0027 item 14
// activation gate, it has its own root and registry, and it cannot reach the
// wiki, the ingest pipeline, the MCP server, or the production archive. The
// production gate is unchanged; this package only runs what ADR 0028 approves.
//
// Its safety comes from what it refuses, in this order of importance:
//
//   - a trial root that overlaps, contains, or sits inside a protected path, or
//     that is not empty and not marked as a trial root, is refused at start;
//   - only https URLs on an exact-host allowlist are fetched, on the first URL
//     and on every redirect, and only a body that starts with the PDF header is
//     stored; the worker re-checks every claimed job against this policy, so a
//     job queued by another route (the `archive job submit` command) cannot
//     widen it;
//   - the number of queued jobs, submissions and requests per period, and the
//     time between requests, are bounded by values this package caps.
package trial

import (
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AckPhrase is the sentence an operator must put in COGVAULT_TRIAL_ACK. Copying
// it is the explicit act of accepting that the trial has no backup.
const AckPhrase = "NO-BACKUP-TRIAL: I accept that every original and job record in this trial is lost if the NAS fails"

// The names of the settings. They are environment variables so that the worker
// and a command run by `docker exec` in the same container read one policy.
const (
	envPrefix = "COGVAULT_TRIAL_"

	EnvRoot             = envPrefix + "ROOT"
	EnvAck              = envPrefix + "ACK"
	EnvAcceptedBy       = envPrefix + "ACCEPTED_BY"
	EnvAllowedHosts     = envPrefix + "ALLOWED_HOSTS"
	EnvForbiddenPaths   = envPrefix + "FORBIDDEN_PATHS"
	EnvMaxQueued        = envPrefix + "MAX_QUEUED"
	EnvMaxSubmitsPerDay = envPrefix + "MAX_SUBMITS_PER_DAY"
	EnvMaxFetchesHour   = envPrefix + "MAX_FETCHES_PER_HOUR"
	EnvMaxFetchesDay    = envPrefix + "MAX_FETCHES_PER_DAY"
	EnvMinInterval      = envPrefix + "MIN_INTERVAL"
	EnvMaxAttempts      = envPrefix + "MAX_ATTEMPTS"
	EnvMaxBytes         = envPrefix + "MAX_BYTES"
)

// Hard caps. A setting above one is refused, so no configuration can turn the
// trial into a crawler or a bulk downloader.
const (
	CapMaxQueued        = 50
	CapMaxSubmitsPerDay = 100
	CapMaxFetchesHour   = 60
	CapMaxFetchesDay    = 200
	CapMinIntervalFloor = 2 * time.Second
	CapMaxAttempts      = 5
	CapMaxBytes         = 32 << 20
	CapHosts            = 10
)

// Defaults for the optional settings.
const (
	DefaultRoot         = "/trial/data"
	DefaultMaxQueued    = 10
	DefaultSubmitsDay   = 20
	DefaultFetchesHour  = 20
	DefaultFetchesDay   = 60
	DefaultMinInterval  = 10 * time.Second
	DefaultMaxAttempts  = 3
	DefaultMaxBytes     = CapMaxBytes
	maxAcceptedByLength = 120
)

// Config is the policy of one trial. Build it with FromEnviron.
type Config struct {
	// Root is the trial's own archive root: an absolute, clean path.
	Root string
	// AcceptedBy names who accepted the no-backup terms (free text, recorded in
	// the root's marker).
	AcceptedBy string
	// AllowedHosts are the exact lowercase host names that may be fetched.
	AllowedHosts []string
	// ForbiddenPaths are protected paths the root must not overlap. The built-in
	// ones are always included; these are extra.
	ForbiddenPaths []string

	MaxQueued        int
	MaxSubmitsPerDay int
	MaxFetchesHour   int
	MaxFetchesDay    int
	MinInterval      time.Duration
	MaxAttempts      int
	MaxBytes         int64
}

// BuiltinForbidden are paths of the production deployment. The trial root must
// not overlap any of them. Inside the trial container none of them exists; the
// list is for a root chosen on a host that does see them.
var BuiltinForbidden = []string{
	"/volume1/docker/cogvault", // the whole production install directory on the NAS
	"/data",                    // the production container's data mount
	"/config",
	"/evidence",
}

var knownEnv = map[string]bool{
	EnvRoot: true, EnvAck: true, EnvAcceptedBy: true, EnvAllowedHosts: true, EnvForbiddenPaths: true,
	EnvMaxQueued: true, EnvMaxSubmitsPerDay: true, EnvMaxFetchesHour: true, EnvMaxFetchesDay: true,
	EnvMinInterval: true, EnvMaxAttempts: true, EnvMaxBytes: true,
}

var hostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// ErrConfig is a refused trial setting. Its text names the setting and the
// class of the problem, never the value.
var ErrConfig = errors.New("invalid trial setting")

func configErr(key, why string) error { return fmt.Errorf("%w: %s: %s", ErrConfig, key, why) }

// FromEnviron builds and validates a Config from "KEY=VALUE" strings (os.Environ).
// A COGVAULT_TRIAL_ variable that is not a known setting is refused, so a typo
// cannot silently leave a limit at its default.
func FromEnviron(environ []string) (Config, error) {
	env := map[string]string{}
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(k, envPrefix) {
			continue
		}
		if !knownEnv[k] {
			return Config{}, configErr(k, "not a known trial setting")
		}
		env[k] = v
	}
	var c Config
	if env[EnvAck] != AckPhrase {
		return Config{}, configErr(EnvAck, "must be exactly the acceptance sentence (see the trial documentation)")
	}
	c.AcceptedBy = strings.TrimSpace(env[EnvAcceptedBy])
	if c.AcceptedBy == "" || len([]rune(c.AcceptedBy)) > maxAcceptedByLength || strings.ContainsAny(c.AcceptedBy, "\r\n\x00") {
		return Config{}, configErr(EnvAcceptedBy, "required, one line, at most 120 characters")
	}
	c.Root = env[EnvRoot]
	if c.Root == "" {
		c.Root = DefaultRoot
	}
	if err := checkAbsClean(EnvRoot, c.Root); err != nil {
		return Config{}, err
	}
	if strings.Count(strings.Trim(c.Root, "/"), "/") < 1 {
		return Config{}, configErr(EnvRoot, "must be at least two levels deep")
	}
	hosts, err := parseHosts(env[EnvAllowedHosts])
	if err != nil {
		return Config{}, err
	}
	c.AllowedHosts = hosts
	paths, err := parseForbidden(env[EnvForbiddenPaths])
	if err != nil {
		return Config{}, err
	}
	c.ForbiddenPaths = paths
	if err := parseLimits(env, &c); err != nil {
		return Config{}, err
	}
	return c, nil
}

func parseForbidden(raw string) ([]string, error) {
	var out []string
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		if err := checkAbsClean(EnvForbiddenPaths, p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// parseLimits fills the numeric limits from env, each within its hard cap.
func parseLimits(env map[string]string, c *Config) error {
	for _, f := range []struct {
		key string
		dst *int
		def int
		max int
	}{
		{EnvMaxQueued, &c.MaxQueued, DefaultMaxQueued, CapMaxQueued},
		{EnvMaxSubmitsPerDay, &c.MaxSubmitsPerDay, DefaultSubmitsDay, CapMaxSubmitsPerDay},
		{EnvMaxFetchesHour, &c.MaxFetchesHour, DefaultFetchesHour, CapMaxFetchesHour},
		{EnvMaxFetchesDay, &c.MaxFetchesDay, DefaultFetchesDay, CapMaxFetchesDay},
		{EnvMaxAttempts, &c.MaxAttempts, DefaultMaxAttempts, CapMaxAttempts},
	} {
		n, err := parseInt(env, f.key, f.def, 1, f.max)
		if err != nil {
			return err
		}
		*f.dst = n
	}
	c.MinInterval = DefaultMinInterval
	if v := env[EnvMinInterval]; v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < CapMinIntervalFloor || d > 24*time.Hour {
			return configErr(EnvMinInterval, "a duration from 2s to 24h")
		}
		c.MinInterval = d
	}
	c.MaxBytes = DefaultMaxBytes
	if v := env[EnvMaxBytes]; v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1024 || n > CapMaxBytes {
			return configErr(EnvMaxBytes, "a byte count from 1024 to 33554432")
		}
		c.MaxBytes = n
	}
	if c.MaxFetchesHour > c.MaxFetchesDay {
		return configErr(EnvMaxFetchesHour, "must not exceed the daily request limit")
	}
	return nil
}

func checkAbsClean(key, p string) error {
	if p == "" || !filepath.IsAbs(p) || filepath.Clean(p) != p || p == "/" || strings.ContainsRune(p, 0) {
		return configErr(key, "must be an absolute, clean path")
	}
	return nil
}

func parseInt(env map[string]string, key string, def, lo, hi int) (int, error) {
	v := env[key]
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < lo || n > hi {
		return 0, configErr(key, fmt.Sprintf("a whole number from %d to %d", lo, hi))
	}
	return n, nil
}

func parseHosts(raw string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, h := range strings.Split(raw, ",") {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if h != strings.ToLower(h) || len(h) > 253 || !hostPattern.MatchString(h) {
			return nil, configErr(EnvAllowedHosts, "each host must be a lowercase DNS name with a dot (no scheme, port, path, wildcard, or address)")
		}
		if _, err := netip.ParseAddr(h); err == nil {
			return nil, configErr(EnvAllowedHosts, "an IP address is not a host name")
		}
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	if len(out) == 0 {
		return nil, configErr(EnvAllowedHosts, "at least one host is required")
	}
	if len(out) > CapHosts {
		return nil, configErr(EnvAllowedHosts, fmt.Sprintf("at most %d hosts", CapHosts))
	}
	sort.Strings(out)
	return out, nil
}

// HostAllowed reports whether host (any case) is on the allowlist, exactly.
func (c Config) HostAllowed(host string) bool {
	host = strings.ToLower(host)
	for _, h := range c.AllowedHosts {
		if h == host {
			return true
		}
	}
	return false
}
