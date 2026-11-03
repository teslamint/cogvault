package archive

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Deletion follows ADR 0027 item 11: a request first gets an impact report;
// an accepted request moves the object to quarantine, where it stays
// restorable; purge needs a separate per-object confirmation and is allowed
// only after QuarantineMinimum. Nothing purges automatically.
//
// Every action is first appended to deletions/log.jsonl, a durable record
// outside the registry that holds no original content. The registry and
// the files are then brought in line with the log. Open re-applies the log,
// so a crash between the two steps, or a registry restored from an older
// backup, cannot bring back a deleted original.

// QuarantineMinimum is how long a quarantined object stays restorable before
// it becomes eligible for purge.
const QuarantineMinimum = 30 * 24 * time.Hour

const deletionLogName = "log.jsonl"

const maxDeletionRecord = 64 << 10

// DeletionRecord is one deletion action. Seq is its position in the
// append-only log: 1, 2, 3, ... with no gaps. The newest action for an
// object is the one with the highest Seq; At is for people and for the
// purge waiting period, never for ordering, so a clock that moves backward
// cannot make an older restore win over a later quarantine or purge.
type DeletionRecord struct {
	Version int   `json:"version"`
	Seq     int64 `json:"seq"`
	// Action is quarantine, restore, or purge.
	Action     string   `json:"action"`
	ObjectID   string   `json:"object_id"`
	CaptureIDs []string `json:"capture_ids"`
	At         string   `json:"at"`
}

func (r DeletionRecord) time() time.Time {
	t, _ := time.Parse(time.RFC3339Nano, r.At)
	return t
}

// Impact lists what depends on an object before an operator acts on it.
type Impact struct {
	ObjectID string `json:"object_id"`
	State    string `json:"state"`
	// Captures are every capture of the object's bytes.
	Captures []string `json:"captures"`
	// Reports cite one of the captures; Summaries summarize one.
	Reports   []string `json:"reports"`
	Summaries []string `json:"summaries"`
	// Attempts name one of the captures as the recovered or the recovering
	// capture.
	Attempts []int64 `json:"attempts"`
	// Manifests are hand-off manifests a quarantine removes. Copies already
	// taken by the ingest host and its source pages stay; they are listed
	// so the operator can act on them there.
	Manifests []string `json:"manifests"`
	// DerivedObjects are processed files stored from these captures (for
	// example a fetch .md). Deletion actions apply to them too.
	DerivedObjects []string `json:"derived_objects"`
	IngestCopies   []string `json:"ingest_copies"`
	SourcePages    []string `json:"source_pages"`
	QuarantinedAt  string   `json:"quarantined_at,omitempty"`
	PurgeEligible  string   `json:"purge_eligible_at,omitempty"`
	// Digest identifies this exact report; Quarantine refuses a stale one.
	Digest string `json:"digest"`
}

// ResolveObject accepts an object ID or a capture ID.
func (a *Archive) ResolveObject(ctx context.Context, id string) (string, error) {
	if objectIDPattern.MatchString(id) {
		var n int
		if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM objects WHERE object_id = ?`, id).Scan(&n); err != nil {
			return "", err
		}
		if n == 0 {
			return "", fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return id, nil
	}
	var obj string
	err := a.db.QueryRowContext(ctx, `SELECT object_id FROM captures WHERE capture_id = ?`, id).Scan(&obj)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return obj, err
}

// ImpactOf reports what depends on the object. It changes nothing.
func (a *Archive) ImpactOf(ctx context.Context, objectID string) (Impact, error) {
	im := Impact{ObjectID: objectID}
	var stateAt string
	err := a.db.QueryRowContext(ctx, `SELECT state, state_at FROM objects WHERE object_id = ?`, objectID).Scan(&im.State, &stateAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Impact{}, fmt.Errorf("%w: %s", ErrNotFound, objectID)
	}
	if err != nil {
		return Impact{}, err
	}
	if im.State == "quarantined" {
		im.QuarantinedAt = stateAt
		if t, err := time.Parse(time.RFC3339Nano, stateAt); err == nil {
			im.PurgeEligible = t.Add(QuarantineMinimum).UTC().Format(time.RFC3339Nano)
		}
	}
	queries := []struct {
		dst   *[]string
		query string
	}{
		{&im.Captures, `SELECT capture_id FROM captures WHERE object_id = ? ORDER BY capture_id`},
		{&im.Reports, `SELECT DISTINCT t.report_id FROM citations t JOIN captures c ON c.capture_id = t.cited_capture_id WHERE c.object_id = ? ORDER BY t.report_id`},
		{&im.Summaries, `SELECT s.capture_id FROM captures s JOIN captures c ON c.capture_id = s.summary_of WHERE c.object_id = ? ORDER BY s.capture_id`},
		{&im.Manifests, `SELECT h.capture_id FROM handoffs h WHERE h.object_id = ? ORDER BY h.capture_id`},
	}
	for _, q := range queries {
		if *q.dst, err = a.ids(ctx, q.query, objectID); err != nil {
			return Impact{}, err
		}
	}
	rows, err := a.db.QueryContext(ctx, `SELECT DISTINCT t.attempt_id FROM attempts t JOIN captures c ON c.capture_id = t.capture_id OR c.capture_id = t.result_capture_id WHERE c.object_id = ? ORDER BY t.attempt_id`, objectID)
	if err != nil {
		return Impact{}, err
	}
	im.Attempts = []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return Impact{}, err
		}
		im.Attempts = append(im.Attempts, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Impact{}, err
	}
	bodies, err := a.ids(ctx, `SELECT r.body FROM receipts r JOIN captures c ON c.capture_id = r.capture_id WHERE c.object_id = ? ORDER BY r.name`, objectID)
	if err != nil {
		return Impact{}, err
	}
	copies, pages := map[string]bool{}, map[string]bool{}
	for _, b := range bodies {
		var r Receipt
		if json.Unmarshal([]byte(b), &r) == nil {
			copies[r.SourcePath] = true
			if r.WikiPage != "" {
				pages[r.WikiPage] = true
			}
		}
	}
	im.IngestCopies, im.SourcePages = sortedKeys(copies), sortedKeys(pages)
	// The derived objects the next action would move: purge for a
	// quarantined object, quarantine otherwise.
	next := "quarantine"
	if im.State != "active" {
		next = "purge"
	}
	if im.DerivedObjects, err = a.derivedObjects(ctx, objectID, next); err != nil {
		return Impact{}, err
	}
	canonical, err := json.Marshal(im)
	if err != nil {
		return Impact{}, err
	}
	sum := sha256.Sum256(canonical)
	im.Digest = hex.EncodeToString(sum[:8])
	return im, nil
}

func sortedKeys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Quarantine moves an active object out of every capture tool, status view,
// and hand-off. digest must match the current ImpactOf report, so an
// operator acts only on the impact they saw.
func (a *Archive) Quarantine(ctx context.Context, objectID, digest string) error {
	unlock, err := a.lock(true)
	if err != nil {
		return err
	}
	defer unlock()
	im, err := a.ImpactOf(ctx, objectID)
	if err != nil {
		return err
	}
	if im.State != "active" {
		return fmt.Errorf("%w: %s is %s", ErrInvalid, objectID, im.State)
	}
	if digest != im.Digest {
		return fmt.Errorf("%w: impact report %q is stale or wrong; the current one is %s", ErrConflict, digest, im.Digest)
	}
	return a.record(ctx, DeletionRecord{Action: "quarantine", ObjectID: objectID, CaptureIDs: im.Captures})
}

// Unquarantine restores a quarantined object.
func (a *Archive) Unquarantine(ctx context.Context, objectID string) error {
	unlock, err := a.lock(true)
	if err != nil {
		return err
	}
	defer unlock()
	im, err := a.ImpactOf(ctx, objectID)
	if err != nil {
		return err
	}
	if im.State != "quarantined" {
		return fmt.Errorf("%w: %s is %s, not quarantined", ErrInvalid, objectID, im.State)
	}
	// A restore record needs intact bytes for the object and for every
	// derived object the restore brings back: one of its quarantined or
	// active files must match its size and SHA-256 (moveIfPresent then keeps
	// the verified copy). Otherwise the restore would mark it active with
	// missing or damaged bytes behind it.
	derived, err := a.derivedObjects(ctx, objectID, "restore")
	if err != nil {
		return err
	}
	for _, id := range append([]string{objectID}, derived...) {
		var size int64
		if err := a.db.QueryRowContext(ctx, `SELECT size FROM objects WHERE object_id = ?`, id).Scan(&size); err != nil {
			return err
		}
		hexSum := strings.TrimPrefix(id, "sha256:")
		intact := false
		for _, p := range []string{filepath.Join(a.root, "quarantine", hexSum), a.objectPath(hexSum)} {
			if verifyFile(p, hexSum, size) == nil {
				intact = true
				break
			}
		}
		if !intact {
			return fmt.Errorf("%w: %s has no intact quarantined or active file to restore", ErrIntegrity, id)
		}
	}
	return a.record(ctx, DeletionRecord{Action: "restore", ObjectID: objectID, CaptureIDs: im.Captures})
}

// Purge removes a quarantined object's bytes for good. confirm must repeat
// the object ID, and the object must have been quarantined for at least
// QuarantineMinimum. Capture metadata stays, marked purged.
func (a *Archive) Purge(ctx context.Context, objectID, confirm string) error {
	if confirm != objectID {
		return fmt.Errorf("%w: purge needs --confirm with the exact object ID", ErrInvalid)
	}
	unlock, err := a.lock(true)
	if err != nil {
		return err
	}
	defer unlock()
	im, err := a.ImpactOf(ctx, objectID)
	if err != nil {
		return err
	}
	if im.State != "quarantined" {
		return fmt.Errorf("%w: only a quarantined object can be purged; %s is %s", ErrInvalid, objectID, im.State)
	}
	since, err := time.Parse(time.RFC3339Nano, im.QuarantinedAt)
	if err != nil {
		return fmt.Errorf("%w: quarantine time %q", ErrIntegrity, im.QuarantinedAt)
	}
	if a.now().Sub(since) < QuarantineMinimum {
		return fmt.Errorf("%w: %s stays restorable until %s", ErrInvalid, objectID, im.PurgeEligible)
	}
	return a.record(ctx, DeletionRecord{Action: "purge", ObjectID: objectID, CaptureIDs: im.Captures})
}

// record appends rec to the durable log with the next sequence number, then
// applies it. The caller holds the exclusive archive lock, so sequence
// numbers are assigned one at a time across processes.
func (a *Archive) record(ctx context.Context, rec DeletionRecord) error {
	path := filepath.Join(a.root, "deletions", deletionLogName)
	prior, err := ReadDeletionLog(path)
	if err != nil {
		return err
	}
	rec.Version = 1
	rec.Seq = int64(len(prior)) + 1
	rec.At = a.now().UTC().Format(time.RFC3339Nano)
	if err := appendDeletion(path, rec); err != nil {
		return err
	}
	return a.apply(ctx, rec)
}

func appendDeletion(path string, rec DeletionRecord) error {
	line, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	// ReadDeletionLog reads lines up to maxDeletionRecord; a longer line
	// would make the whole log unreadable, so it is refused before writing.
	if len(line)+1 > maxDeletionRecord {
		return fmt.Errorf("%w: deletion record for %s is %d bytes (%d capture IDs); the limit is %d", ErrTooLarge, rec.ObjectID, len(line)+1, len(rec.CaptureIDs), maxDeletionRecord)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE|noFollow, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

// ReadDeletionLog reads a deletion log. A missing file is an empty log; a
// malformed line is an error, so a damaged log is never half applied.
func ReadDeletionLog(path string) ([]DeletionRecord, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|noFollow, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []DeletionRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 4096), maxDeletionRecord)
	for n := 1; sc.Scan(); n++ {
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var r DeletionRecord
		if err := decodeStrict(sc.Bytes(), &r); err != nil {
			return nil, fmt.Errorf("deletion log %s line %d: %w", path, n, err)
		}
		if r.Version != 1 || !objectIDPattern.MatchString(r.ObjectID) || !oneOf(r.Action, []string{"quarantine", "restore", "purge"}) || r.time().IsZero() {
			return nil, fmt.Errorf("%w: deletion log %s line %d has invalid fields", ErrInvalid, path, n)
		}
		if r.Seq != int64(len(out))+1 {
			return nil, fmt.Errorf("%w: deletion log %s line %d has seq %d, want %d", ErrInvalid, path, n, r.Seq, len(out)+1)
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

// mergeDeletionLogs combines logs that share one sequence: an older copy (a
// backup's log) is a prefix of a newer one (the live log). The same seq with
// different content means the logs diverged, which is an error rather than
// a guess.
func mergeDeletionLogs(logs ...[]DeletionRecord) ([]DeletionRecord, error) {
	var out []DeletionRecord
	for _, log := range logs {
		for i, r := range log {
			if i < len(out) {
				if !sameRecord(out[i], r) {
					return nil, fmt.Errorf("%w: deletion logs disagree at seq %d", ErrConflict, r.Seq)
				}
				continue
			}
			out = append(out, r)
		}
	}
	return out, nil
}

func sameRecord(a, b DeletionRecord) bool {
	if a.Seq != b.Seq || a.Action != b.Action || a.ObjectID != b.ObjectID || a.At != b.At || len(a.CaptureIDs) != len(b.CaptureIDs) {
		return false
	}
	for i := range a.CaptureIDs {
		if a.CaptureIDs[i] != b.CaptureIDs[i] {
			return false
		}
	}
	return true
}

// latestDeletions returns the record with the highest seq per object.
func latestDeletions(recs []DeletionRecord) map[string]DeletionRecord {
	out := map[string]DeletionRecord{}
	for _, r := range recs {
		if prev, ok := out[r.ObjectID]; !ok || r.Seq > prev.Seq {
			out[r.ObjectID] = r
		}
	}
	return out
}

// ReplicaCovers checks that the deletion log replica holds every record of
// the live log under root, in the same order (the live log is a prefix of
// the replica). It checks one moment only; keeping the replica current is
// the operator's job.
func ReplicaCovers(replica, root string) error {
	rep, err := ReadDeletionLog(replica)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(replica); err != nil {
		return fmt.Errorf("%w: deletion log replica: %v", ErrInvalid, err)
	}
	live, err := ReadDeletionLog(filepath.Join(root, "deletions", deletionLogName))
	if err != nil {
		return err
	}
	if len(rep) < len(live) {
		return fmt.Errorf("%w: deletion log replica has %d records, the live log has %d", ErrConflict, len(rep), len(live))
	}
	_, err = mergeDeletionLogs(rep, live)
	return err
}

// applyDeletions brings the registry and files in line with the log.
func (a *Archive) applyDeletions(ctx context.Context) (int, error) {
	recs, err := ReadDeletionLog(filepath.Join(a.root, "deletions", deletionLogName))
	if err != nil {
		return 0, err
	}
	latest := latestDeletions(recs)
	ids := sortedKeys(func() map[string]bool {
		m := map[string]bool{}
		for k := range latest {
			m[k] = true
		}
		return m
	}())
	for _, id := range ids {
		if err := a.apply(ctx, latest[id]); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

// apply makes one object's registry state and file location match rec. It
// is idempotent, so re-applying the whole log is safe.
func (a *Archive) apply(ctx context.Context, rec DeletionRecord) error {
	derived, err := a.derivedObjects(ctx, rec.ObjectID, rec.Action)
	if err != nil {
		return err
	}
	for _, id := range append([]string{rec.ObjectID}, derived...) {
		if err := a.applyObject(ctx, id, rec.Action, rec.At); err != nil {
			return err
		}
	}
	return syncDir(filepath.Join(a.root, "quarantine"))
}

// applyObject moves one object's file and sets its state for action.
func (a *Archive) applyObject(ctx context.Context, objectID, action, at string) error {
	hexSum := strings.TrimPrefix(objectID, "sha256:")
	active := a.objectPath(hexSum)
	quarantined := filepath.Join(a.root, "quarantine", hexSum)
	state := map[string]string{"quarantine": "quarantined", "restore": "active", "purge": "purged"}[action]
	size := int64(-1) // unknown: the registry has no row for this object
	if err := a.db.QueryRowContext(ctx, `SELECT size FROM objects WHERE object_id = ?`, objectID).Scan(&size); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	switch action {
	case "quarantine":
		if err := moveIfPresent(active, quarantined, hexSum, size); err != nil {
			return err
		}
		caps, err := a.ids(ctx, `SELECT capture_id FROM captures WHERE object_id = ?`, objectID)
		if err != nil {
			return err
		}
		for _, c := range caps {
			if err := os.Remove(filepath.Join(a.root, "outbox", c+".json")); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	case "restore":
		if err := moveIfPresent(quarantined, active, hexSum, size); err != nil {
			return err
		}
	case "purge":
		for _, p := range []string{active, quarantined} {
			if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		}
	}
	_, err := a.db.ExecContext(ctx, `UPDATE objects SET state = ?, state_at = ? WHERE object_id = ?`, state, at, objectID)
	return err
}

// moveIfPresent renames src to dst when src exists and dst does not. If
// both exist (for example after a restored backup), the verified copy wins
// and the other is removed, so an object never lives in two places: dst is
// kept when its size and SHA-256 match, otherwise a matching src replaces
// it, and when neither matches nothing is removed and ErrIntegrity is
// returned. With an unknown size (size < 0) dst is kept unverified. A
// missing src is not an error and dst is not checked: replaying the log on
// open must not fail on an earlier move. Unquarantine verifies the bytes
// before it records a restore, and restore-test re-hashes active objects.
func moveIfPresent(src, dst, hexSum string, size int64) error {
	if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := os.Lstat(dst); err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return os.Rename(src, dst)
	}
	if size < 0 || verifyFile(dst, hexSum, size) == nil {
		return os.Remove(src)
	}
	if err := verifyFile(src, hexSum, size); err != nil {
		return fmt.Errorf("%w: neither %s nor %s holds the object's bytes", ErrIntegrity, src, dst)
	}
	return os.Rename(src, dst)
}
