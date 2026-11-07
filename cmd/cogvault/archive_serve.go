package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/teslamint/cogvault/internal/archive"
	"github.com/teslamint/cogvault/internal/archivegate"
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
			var ae *archivegate.Error
			if errors.As(err, &ae) {
				return reject(ae.Code, ae.Keys)
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
