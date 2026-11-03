package archive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const noFollow = unix.O_NOFOLLOW

// Backup copies one consistent archive state into a new directory (ADR
// 0027 item 13). The registry copy comes from VACUUM INTO, never from a
// live WAL file. Objects, quarantined objects, uncollected receipts, and
// the deletion log are copied and listed with their hashes in backup.json.
//
// Backup does not choose a target, a schedule, or retention: the off-NAS
// copy, the daily schedule, and the 30-day expiry of backup copies belong
// to the operator's backup tool and are deployment settings.

const backupManifestName = "backup.json"

type BackupFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type BackupManifest struct {
	Version   int          `json:"version"`
	CreatedAt string       `json:"created_at"`
	Files     []BackupFile `json:"files"`
}

// backupDirs are copied file by file. outbox/ is not: manifests are
// rewritten by Handoff from the registry.
var backupDirs = []string{filepath.Join("objects", "sha256"), "quarantine", "receipts", "deletions"}

// Backup writes a backup of the archive into dest, which must not exist.
// It holds the exclusive archive lock, so no capture, hand-off, or deletion
// in any process changes the archive between the registry snapshot and the
// file copies. Before it writes backup.json it checks the copy: every
// object the registry snapshot holds as active or quarantined must be in
// the backup with its size and hash. A mismatch fails the backup instead of
// writing a manifest for an inconsistent copy.
func (a *Archive) Backup(ctx context.Context, dest string) (BackupManifest, error) {
	if !filepath.IsAbs(dest) {
		return BackupManifest{}, fmt.Errorf("%w: backup destination %q must be absolute", ErrInvalid, dest)
	}
	unlock, err := a.lock(true)
	if err != nil {
		return BackupManifest{}, err
	}
	defer unlock()
	if err := os.Mkdir(dest, 0o700); err != nil {
		return BackupManifest{}, fmt.Errorf("backup destination: %w", err)
	}
	for _, d := range backupDirs {
		if err := os.MkdirAll(filepath.Join(dest, d), 0o700); err != nil {
			return BackupManifest{}, err
		}
	}
	if _, err := a.db.ExecContext(ctx, `VACUUM INTO ?`, filepath.Join(dest, "registry.db")); err != nil {
		return BackupManifest{}, fmt.Errorf("backup registry: %w", err)
	}
	for _, d := range backupDirs {
		entries, err := os.ReadDir(filepath.Join(a.root, d))
		if err != nil {
			return BackupManifest{}, err
		}
		for _, e := range entries {
			if !e.Type().IsRegular() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			if err := copyFile(filepath.Join(a.root, d, e.Name()), filepath.Join(dest, d, e.Name())); err != nil {
				return BackupManifest{}, err
			}
		}
	}
	if err := checkBackupObjects(ctx, dest); err != nil {
		return BackupManifest{}, err
	}
	m, err := hashTree(dest)
	if err != nil {
		return BackupManifest{}, err
	}
	m.Version, m.CreatedAt = 1, a.now().UTC().Format(time.RFC3339Nano)
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return BackupManifest{}, err
	}
	if _, err := writeOnce(dest, backupManifestName, body); err != nil {
		return BackupManifest{}, err
	}
	return m, nil
}

// checkBackupObjects opens the copied registry read-only and checks that
// every active object is in objects/sha256 and every quarantined object is
// in quarantine/, each with the recorded size and its hash.
func checkBackupObjects(ctx context.Context, dir string) error {
	dsn := (&url.URL{Scheme: "file", Path: filepath.Join(dir, "registry.db"), RawQuery: "mode=ro"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT object_id, size, state FROM objects WHERE state IN ('active','quarantined')`)
	if err != nil {
		return fmt.Errorf("backup registry copy: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, state string
		var size int64
		if err := rows.Scan(&id, &size, &state); err != nil {
			return err
		}
		hexSum := strings.TrimPrefix(id, "sha256:")
		where := filepath.Join(dir, "objects", "sha256", hexSum)
		if state == "quarantined" {
			where = filepath.Join(dir, "quarantine", hexSum)
		}
		if err := verifyFile(where, hexSum, size); err != nil {
			return fmt.Errorf("%w: backup is inconsistent: %s object %s: %v", ErrIntegrity, state, id, err)
		}
	}
	return rows.Err()
}

func copyFile(src, dst string) error {
	in, err := os.OpenFile(src, os.O_RDONLY|noFollow, 0)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL|noFollow, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// hashTree lists every regular file under dir except the manifest.
func hashTree(dir string) (BackupManifest, error) {
	var m BackupManifest
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s is a symlink", ErrIntegrity, p)
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil || rel == backupManifestName {
			return err
		}
		f, err := os.OpenFile(p, os.O_RDONLY|noFollow, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		h := sha256.New()
		n, err := io.Copy(h, f)
		if err != nil {
			return err
		}
		m.Files = append(m.Files, BackupFile{Path: filepath.ToSlash(rel), Size: n, SHA256: hex.EncodeToString(h.Sum(nil))})
		return nil
	})
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	return m, err
}

// VerifyBackup checks that dir holds exactly the files its manifest lists,
// with the listed sizes and hashes.
func VerifyBackup(dir string) (BackupManifest, error) {
	data, err := readBounded(filepath.Join(dir, backupManifestName), 64<<20)
	if err != nil {
		return BackupManifest{}, fmt.Errorf("backup manifest: %w", err)
	}
	var want BackupManifest
	if err := decodeStrict(data, &want); err != nil {
		return BackupManifest{}, err
	}
	got, err := hashTree(dir)
	if err != nil {
		return BackupManifest{}, err
	}
	if len(got.Files) != len(want.Files) {
		return BackupManifest{}, fmt.Errorf("%w: backup has %d files, manifest lists %d", ErrIntegrity, len(got.Files), len(want.Files))
	}
	for i := range want.Files {
		if got.Files[i] != want.Files[i] {
			return BackupManifest{}, fmt.Errorf("%w: backup file %s does not match its manifest", ErrIntegrity, want.Files[i].Path)
		}
	}
	return want, nil
}

// RestoreReport is the evidence a restore test leaves. Production capture
// requires a passed report (ADR 0027 item 14).
type RestoreReport struct {
	Version   int    `json:"version"`
	Passed    bool   `json:"passed"`
	Backup    string `json:"backup"`
	CreatedAt string `json:"backup_created_at"`
	CheckedAt string `json:"checked_at"`
	// ObjectsVerified active objects were re-hashed after the restore.
	ObjectsVerified int `json:"objects_verified"`
	// DeletionsReapplied objects had a deletion record re-applied.
	DeletionsReapplied int `json:"deletions_reapplied"`
	// Resurrected lists quarantined or purged objects that came back as
	// active after the restore. A passed report has none.
	Resurrected []string `json:"resurrected"`
	Errors      []string `json:"errors"`
	// DeletionLogs are the logs merged with the backup's own log, as they
	// were when the test read them. A report whose only log is the archive's
	// live log shows nothing about restoring after the archive host is lost;
	// production activation requires the configured replica here.
	DeletionLogs []DeletionLogUse `json:"deletion_logs"`
}

type DeletionLogUse struct {
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Records int    `json:"records"`
}

func describeLog(p string) (DeletionLogUse, error) {
	recs, err := ReadDeletionLog(p)
	if err != nil {
		return DeletionLogUse{}, err
	}
	data, err := readBounded(p, 1<<30)
	if err != nil {
		return DeletionLogUse{}, err
	}
	sum := sha256.Sum256(data)
	return DeletionLogUse{Path: filepath.Clean(p), SHA256: hex.EncodeToString(sum[:]), Records: len(recs)}, nil
}

// Restore copies a verified backup into target (which must not exist),
// merges the backup's deletion log with extraLogs (for example the live
// log, which may be newer than the backup), and opens the result, which
// re-applies every deletion. The caller closes the returned archive.
func Restore(backupDir, target string, extraLogs []string) (*Archive, BackupManifest, error) {
	m, err := VerifyBackup(backupDir)
	if err != nil {
		return nil, BackupManifest{}, err
	}
	if !filepath.IsAbs(target) {
		return nil, BackupManifest{}, fmt.Errorf("%w: restore target %q must be absolute", ErrInvalid, target)
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		return nil, BackupManifest{}, fmt.Errorf("restore target: %w", err)
	}
	for _, f := range m.Files {
		dst := filepath.Join(target, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return nil, BackupManifest{}, err
		}
		if err := copyFile(filepath.Join(backupDir, filepath.FromSlash(f.Path)), dst); err != nil {
			return nil, BackupManifest{}, err
		}
	}
	logPath := filepath.Join(target, "deletions", deletionLogName)
	merged, err := ReadDeletionLog(logPath)
	if err != nil {
		return nil, BackupManifest{}, err
	}
	for _, p := range extraLogs {
		recs, err := ReadDeletionLog(p)
		if err != nil {
			return nil, BackupManifest{}, err
		}
		if merged, err = mergeDeletionLogs(merged, recs); err != nil {
			return nil, BackupManifest{}, err
		}
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return nil, BackupManifest{}, err
	}
	if err := os.Remove(logPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, BackupManifest{}, err
	}
	for _, r := range merged {
		if err := appendDeletion(logPath, r); err != nil {
			return nil, BackupManifest{}, err
		}
	}
	a, err := Open(target)
	if err != nil {
		return nil, BackupManifest{}, err
	}
	return a, m, nil
}

// RestoreTest restores backupDir into a scratch directory under
// scratchParent, re-applies the deletion logs, re-hashes every active
// object, and checks that no quarantined or purged object came back. It
// removes the scratch copy and returns the report.
func RestoreTest(ctx context.Context, backupDir, scratchParent string, liveLogs []string, now time.Time) (RestoreReport, error) {
	rep := RestoreReport{Version: 1, Backup: backupDir, CheckedAt: now.UTC().Format(time.RFC3339Nano), Resurrected: []string{}, Errors: []string{}, DeletionLogs: []DeletionLogUse{}}
	for _, p := range liveLogs {
		use, err := describeLog(p)
		if err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("deletion log %s: %v", p, err))
			return rep, nil
		}
		rep.DeletionLogs = append(rep.DeletionLogs, use)
	}
	scratch, err := os.MkdirTemp(scratchParent, "cogvault-restore-test-*")
	if err != nil {
		return rep, err
	}
	defer os.RemoveAll(scratch)
	a, m, err := Restore(backupDir, filepath.Join(scratch, "archive"), liveLogs)
	if err != nil {
		rep.Errors = append(rep.Errors, err.Error())
		return rep, nil
	}
	defer a.Close()
	rep.CreatedAt = m.CreatedAt

	logs, err := ReadDeletionLog(filepath.Join(a.root, "deletions", deletionLogName))
	if err != nil {
		rep.Errors = append(rep.Errors, err.Error())
		return rep, nil
	}
	latest := latestDeletions(logs)
	rep.DeletionsReapplied = len(latest)
	for id, r := range latest {
		if r.Action == "restore" {
			continue
		}
		derived, err := a.derivedObjects(ctx, id)
		if err != nil {
			rep.Errors = append(rep.Errors, err.Error())
			return rep, nil
		}
		for _, obj := range append([]string{id}, derived...) {
			var state string
			_ = a.db.QueryRowContext(ctx, `SELECT state FROM objects WHERE object_id = ?`, obj).Scan(&state)
			_, statErr := os.Lstat(a.objectPath(strings.TrimPrefix(obj, "sha256:")))
			if state == "active" || statErr == nil {
				rep.Resurrected = append(rep.Resurrected, obj)
			}
		}
	}
	sort.Strings(rep.Resurrected)
	rows, err := a.db.QueryContext(ctx, `SELECT object_id, size FROM objects WHERE state = 'active' ORDER BY object_id`)
	if err != nil {
		rep.Errors = append(rep.Errors, err.Error())
		return rep, nil
	}
	type obj struct {
		id   string
		size int64
	}
	var objs []obj
	for rows.Next() {
		var o obj
		if err := rows.Scan(&o.id, &o.size); err != nil {
			rows.Close()
			rep.Errors = append(rep.Errors, err.Error())
			return rep, nil
		}
		objs = append(objs, o)
	}
	rows.Close()
	for _, o := range objs {
		hexSum := strings.TrimPrefix(o.id, "sha256:")
		if err := verifyFile(a.objectPath(hexSum), hexSum, o.size); err != nil {
			rep.Errors = append(rep.Errors, fmt.Sprintf("object %s: %v", o.id, errors.Unwrap(err)))
			continue
		}
		rep.ObjectsVerified++
	}
	rep.Passed = len(rep.Errors) == 0 && len(rep.Resurrected) == 0
	return rep, nil
}

// ReadRestoreReport reads a restore test report and requires that it
// passed.
func ReadRestoreReport(path string) (RestoreReport, error) {
	data, err := readBounded(path, 16<<20)
	if err != nil {
		return RestoreReport{}, fmt.Errorf("restore test report: %w", err)
	}
	var r RestoreReport
	if err := decodeStrict(data, &r); err != nil {
		return RestoreReport{}, fmt.Errorf("restore test report: %w", err)
	}
	if r.Version != 1 || !r.Passed {
		return r, fmt.Errorf("restore test report %s did not pass", path)
	}
	return r, nil
}
