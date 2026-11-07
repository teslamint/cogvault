// Package archivegate is the activation gate every process that captures into
// the archive passes (ADR 0027 item 14): `serve` for the MCP tools and each
// collection worker. It holds the one production check, the worker's wrapper
// around it, and the worker loop. Nothing here relaxes the check, and no
// setting replaces it; tests inject their own check by calling the loop
// directly, which a command cannot do.
package archivegate

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/teslamint/cogvault/internal/adapter"
	"github.com/teslamint/cogvault/internal/archive"
	"github.com/teslamint/cogvault/internal/config"
)

// Error explains a blocked activation with a fixed code and config key names
// only. It never carries a config value: logs must not show paths, issuers,
// or credentials.
type Error struct {
	Code string
	Keys []string
}

func (e *Error) Error() string {
	if len(e.Keys) == 0 {
		return "archive capture activation blocked: " + e.Code
	}
	return fmt.Sprintf("archive capture activation blocked: %s: %s", e.Code, strings.Join(e.Keys, ", "))
}

// ProductionActivation is the only activation check serve and the workers use
// (ADR 0027 item 14). It is a presence check on operator-supplied
// attestations, not a verification of the deployment. It confirms that every
// deployment setting is set, that the restore test report says it passed and
// tested a backup under archive.deployment.backup_target, and that the
// deletion log replica holds every record of the live log right now. It
// cannot see whether the target or the replica is really off the NAS, whether
// backups run daily, whether the replica stays current, whether permissions
// and mounts are right, or how fresh the report is. Enabling capture in
// production still needs a separate human review. Tests inject their own
// check instead; no config key relaxes this one.
func ProductionActivation(cfg *config.Config) error {
	d := cfg.Archive.Deployment
	if missing := d.MissingDeploymentSettings(); len(missing) > 0 {
		return &Error{Code: "deployment_settings_missing", Keys: missing}
	}
	rep, err := archive.ReadRestoreReport(d.RestoreTestReport)
	if err != nil {
		return &Error{Code: "restore_test_not_passed", Keys: []string{"archive.deployment.restore_test_report"}}
	}
	target := filepath.Clean(d.BackupTarget)
	if !filepath.IsAbs(target) || !adapter.HasPathPrefix(filepath.Clean(rep.Backup), target) || filepath.Clean(rep.Backup) == target {
		return &Error{Code: "restore_report_not_for_backup_target", Keys: []string{"archive.deployment.restore_test_report", "archive.deployment.backup_target"}}
	}
	if err := archive.ReplicaCovers(d.DeletionLogReplica, cfg.Archive.Root); err != nil {
		return &Error{Code: "deletion_log_replica_not_current", Keys: []string{"archive.deployment.deletion_log_replica"}}
	}
	usedReplica := false
	for _, l := range rep.DeletionLogs {
		usedReplica = usedReplica || l.Path == filepath.Clean(d.DeletionLogReplica)
	}
	if !usedReplica {
		return &Error{Code: "restore_report_without_replica", Keys: []string{"archive.deployment.restore_test_report", "archive.deployment.deletion_log_replica"}}
	}
	return nil
}

// WorkerAllowed is a worker's activation gate, the same one `serve` uses for
// the capture tools: the config must name this root, capture must be enabled,
// and the activation check must pass. Flipping archive.capture_enabled off in
// the config therefore pauses a worker as well as the MCP tools. The error
// carries a code and key names only. activation is injected so a test can use
// its own check; every command passes ProductionActivation.
func WorkerAllowed(configPath, root string, activation func(*config.Config) error) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		// The load error can name paths or values (an OAuth issuer, a directory);
		// the worker logs this error, so it gets a fixed code and the key to fix.
		return &Error{Code: "config_invalid", Keys: []string{"--config"}}
	}
	if cfg.Archive.Root == "" || filepath.Clean(cfg.Archive.Root) != filepath.Clean(root) {
		return &Error{Code: "worker_root_not_the_configured_archive_root", Keys: []string{"archive.root"}}
	}
	if !cfg.Archive.CaptureEnabled {
		return &Error{Code: "capture_disabled", Keys: []string{"archive.capture_enabled"}}
	}
	return activation(cfg)
}
