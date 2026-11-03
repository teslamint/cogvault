package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/teslamint/cogvault/internal/adapter"
	"github.com/teslamint/cogvault/internal/archive"
	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/index"
	cogmcp "github.com/teslamint/cogvault/internal/mcp"
)

// archiveDrainTimeout bounds how long a disabling reload waits for capture
// calls admitted before the gate closed. It is a prototype value; the
// production bound is ADR 0027 open question 8. When it expires the gate
// stays closed and the remaining calls finish on their own.
const archiveDrainTimeout = 30 * time.Second

// archiveRuntime is serve's archive state (ADR 0027 item 12): the open
// archive, the gate every archive tool enters, and the startup config that
// reloads are compared against. Only the capture-enabled boolean changes at
// runtime, and only through Reload, which only the local SIGHUP handler
// calls; no MCP tool or HTTP route reaches it.
type archiveRuntime struct {
	mu           sync.Mutex // one reload at a time
	configPath   string
	startup      *config.Config
	archive      *archive.Archive
	gate         *cogmcp.ArchiveGate
	srv          *server.MCPServer
	tools        []server.ServerTool
	activation   func(*config.Config) error
	drainTimeout time.Duration
	log          *slog.Logger
}

// activationError explains a blocked activation with a fixed code and
// config key names only. It never carries a config value: reload logs must
// not show paths, issuers, or credentials.
type activationError struct {
	code string
	keys []string
}

func (e *activationError) Error() string {
	if len(e.keys) == 0 {
		return "archive capture activation blocked: " + e.code
	}
	return fmt.Sprintf("archive capture activation blocked: %s: %s", e.code, strings.Join(e.keys, ", "))
}

// productionActivation is the only activation check serve uses (ADR 0027
// item 14). It is a presence check on operator-supplied attestations, not a
// verification of the deployment. It confirms that every deployment setting
// is set, that the restore test report says it passed and tested a backup
// under archive.deployment.backup_target, and that the deletion log replica
// holds every record of the live log right now. It cannot see whether the
// target or the replica is really off the NAS, whether backups run daily,
// whether the replica stays current, whether permissions and mounts are
// right, or how fresh the report is. Enabling capture in production still
// needs a separate human review. Tests inject their own check instead; no
// config key relaxes this one.
func productionActivation(cfg *config.Config) error {
	d := cfg.Archive.Deployment
	if missing := d.MissingDeploymentSettings(); len(missing) > 0 {
		return &activationError{code: "deployment_settings_missing", keys: missing}
	}
	rep, err := archive.ReadRestoreReport(d.RestoreTestReport)
	if err != nil {
		return &activationError{code: "restore_test_not_passed", keys: []string{"archive.deployment.restore_test_report"}}
	}
	target := filepath.Clean(d.BackupTarget)
	if !filepath.IsAbs(target) || !adapter.HasPathPrefix(filepath.Clean(rep.Backup), target) || filepath.Clean(rep.Backup) == target {
		return &activationError{code: "restore_report_not_for_backup_target", keys: []string{"archive.deployment.restore_test_report", "archive.deployment.backup_target"}}
	}
	if err := archive.ReplicaCovers(d.DeletionLogReplica, cfg.Archive.Root); err != nil {
		return &activationError{code: "deletion_log_replica_not_current", keys: []string{"archive.deployment.deletion_log_replica"}}
	}
	usedReplica := false
	for _, l := range rep.DeletionLogs {
		usedReplica = usedReplica || l.Path == filepath.Clean(d.DeletionLogReplica)
	}
	if !usedReplica {
		return &activationError{code: "restore_report_without_replica", keys: []string{"archive.deployment.restore_test_report", "archive.deployment.deletion_log_replica"}}
	}
	return nil
}

// newArchiveRuntime opens the configured archive (even with capture
// disabled, so a later enable needs no restart) and registers the tools if
// capture is enabled and activation allows it. With no archive.root it
// returns nil: there is nothing to reload. A blocked activation at startup
// is an error: serve does not start with capture silently off.
func newArchiveRuntime(cfg *config.Config, configPath string, srv *server.MCPServer, idx index.Index, pages cogmcp.PageChecker, activation func(*config.Config) error, log *slog.Logger) (*archiveRuntime, error) {
	if cfg.Archive.Root == "" {
		return nil, nil
	}
	if cfg.Archive.CaptureEnabled {
		if err := activation(cfg); err != nil {
			return nil, err
		}
	}
	a, err := archive.Open(cfg.Archive.Root)
	if err != nil {
		return nil, err
	}
	gate := &cogmcp.ArchiveGate{}
	r := &archiveRuntime{
		configPath: configPath, startup: cfg, archive: a, gate: gate, srv: srv,
		tools: cogmcp.ArchiveTools(a, gate, idx, pages), activation: activation,
		drainTimeout: archiveDrainTimeout, log: log,
	}
	if cfg.Archive.CaptureEnabled {
		gate.Open()
		srv.AddTools(r.tools...)
	}
	return r, nil
}

func (r *archiveRuntime) Close() error { return r.archive.Close() }

// restartOnlyKeys are the top-level config keys a reload must find
// unchanged. A change to any of them rejects the whole reload.
var restartOnlyKeys = map[string]bool{"wiki_dir": true, "db_path": true, "auth": true}

// archiveRestartOnlyKeys are the archive keys a reload must find unchanged.
var archiveRestartOnlyKeys = map[string]bool{"root": true, "deployment": true}

// configDiff returns the changed keys by YAML name, never their values.
func configDiff(old, cur *config.Config) (restartOnly, notApplied []string) {
	ov, cv := reflect.ValueOf(*old), reflect.ValueOf(*cur)
	t := ov.Type()
	for i := range t.NumField() {
		name := strings.Split(t.Field(i).Tag.Get("yaml"), ",")[0]
		if name == "archive" || reflect.DeepEqual(ov.Field(i).Interface(), cv.Field(i).Interface()) {
			continue
		}
		if restartOnlyKeys[name] {
			restartOnly = append(restartOnly, name)
		} else {
			notApplied = append(notApplied, name)
		}
	}
	oa, ca := reflect.ValueOf(old.Archive), reflect.ValueOf(cur.Archive)
	at := oa.Type()
	for i := range at.NumField() {
		name := strings.Split(at.Field(i).Tag.Get("yaml"), ",")[0]
		if archiveRestartOnlyKeys[name] && !reflect.DeepEqual(oa.Field(i).Interface(), ca.Field(i).Interface()) {
			restartOnly = append(restartOnly, "archive."+name)
		}
	}
	return restartOnly, notApplied
}

// configKeyPattern matches the key prefix config validation errors start
// with ("auth.oauth.issuer: ..."). Only such a prefix reaches the log.
var configKeyPattern = regexp.MustCompile(`^([a-z_]+(\[[0-9]+\])?(\.[a-z_]+(\[[0-9]+\])?)*): `)

// configErrorKey returns the config key a load error names, or "" when the
// error names none (for example a YAML syntax error, whose text may quote
// the file).
func configErrorKey(err error) string {
	if m := configKeyPattern.FindStringSubmatch(err.Error()); m != nil {
		return m[1]
	}
	return ""
}

var errReloadRejected = errors.New("archive reload rejected")

// Reload re-reads and fully validates the config, then applies only
// archive.capture_enabled. Any failure leaves the running state unchanged.
// Disable closes the gate (new calls are refused at once), removes the
// tools, and waits for admitted calls until the drain bound or ctx ends;
// enable checks activation, opens the gate, and adds the tools.
//
// Logs carry a fixed code and key names only, never config values, file
// paths, or credentials. The returned error has the same content.
func (r *archiveRuntime) Reload(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	was := r.gate.IsOpen()
	reject := func(code string, keys []string) error {
		r.log.Error("archive reload rejected", "trigger", "SIGHUP", "code", code, "keys", keys, "capture_enabled", was)
		if len(keys) == 0 {
			return fmt.Errorf("%w: %s", errReloadRejected, code)
		}
		return fmt.Errorf("%w: %s: %s", errReloadRejected, code, strings.Join(keys, ", "))
	}
	cfg, err := config.Load(r.configPath)
	if err != nil {
		var keys []string
		if k := configErrorKey(err); k != "" {
			keys = []string{k}
		}
		return reject("config_invalid", keys)
	}
	restartOnly, notApplied := configDiff(r.startup, cfg)
	if len(restartOnly) > 0 {
		return reject("restart_only_changed", restartOnly)
	}
	if len(notApplied) > 0 {
		r.log.Warn("archive reload: changed keys are not applied until a restart", "trigger", "SIGHUP", "keys", notApplied)
	}
	want := cfg.Archive.CaptureEnabled
	switch {
	case want == was:
		r.log.Info("archive reload: capture unchanged", "trigger", "SIGHUP", "capture_enabled", was)
	case want:
		if err := r.activation(cfg); err != nil {
			var ae *activationError
			if errors.As(err, &ae) {
				return reject(ae.code, ae.keys)
			}
			return reject("activation_blocked", nil)
		}
		r.gate.Open()
		r.srv.AddTools(r.tools...)
		r.log.Info("archive reload: capture enabled", "trigger", "SIGHUP", "old", was, "new", true)
	default:
		drained := r.gate.Close()
		r.srv.DeleteTools(cogmcp.ArchiveToolNames...)
		timer := time.NewTimer(r.drainTimeout)
		defer timer.Stop()
		select {
		case <-drained:
			r.log.Info("archive reload: capture disabled", "trigger", "SIGHUP", "old", was, "new", false)
		case <-timer.C:
			r.log.Warn("archive reload: capture disabled; drain timed out, earlier calls still running", "trigger", "SIGHUP", "in_flight", r.gate.InFlight())
		case <-ctx.Done():
			r.log.Warn("archive reload: capture disabled; drain wait ended by shutdown", "trigger", "SIGHUP", "in_flight", r.gate.InFlight())
		}
	}
	return nil
}
