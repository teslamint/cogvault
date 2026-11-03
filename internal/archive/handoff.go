package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Hand-off moves verified originals from the archive host to the ingest
// host as plain files (ADR 0027 item 9): the archive host writes one
// manifest per capture under outbox/, the ingest host copies objects into a
// source directory and writes receipts under receipts/, and the archive
// host collects those receipts. No host but the archive host opens the
// registry, so Pickup and WriteReceipts read files only.

const (
	manifestVersion = 1
	receiptVersion  = 1
	maxManifestSize = 4 << 10
	maxReceiptSize  = 16 << 10
)

// HandoffExtensions are the source types ingest digests (SPEC §3
// sources[].types); other originals are held.
var HandoffExtensions = []string{"pdf", "md", "markdown", "txt"}

var (
	captureIDPattern    = regexp.MustCompile(`^cap_[0-9a-f]{32}$`)
	objectIDPattern     = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	manifestNamePattern = regexp.MustCompile(`^(cap_[0-9a-f]{32})\.json$`)
	receiptNamePattern  = regexp.MustCompile(`^(cap_[0-9a-f]{32})\.([1-9][0-9]{0,8})\.json$`)
)

// Manifest tells the ingest host which object to copy and under which
// extension. It carries no host path.
type Manifest struct {
	Version   int    `json:"version"`
	CaptureID string `json:"capture_id"`
	ObjectID  string `json:"object_id"`
	Size      int64  `json:"size"`
	Extension string `json:"extension"`
}

type HandoffResult struct {
	CaptureID string `json:"capture_id"`
	ObjectID  string `json:"object_id"`
	// Status is written, existing, or held.
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	// Extension is the object's canonical extension: every capture of one
	// object is handed off under the same extension, so identical bytes get
	// one source path, one ledger key, and one source page (ADR 0027 item 9).
	Extension string `json:"extension,omitempty"`
}

// DefaultHandoffTypes are the types every ingest backend digests: .txt is
// verified only on the openai (text input) backend, so it is handed off
// only when the operator names it.
var DefaultHandoffTypes = []string{"pdf", "md", "markdown"}

// heldReason says why a source capture is not handed off, or "" if it is
// eligible. Unknown, incomplete, and partial originals are kept but never
// handed off automatically (ADR 0027 item 7); no release path exists yet.
func heldReason(acquisition, completeness, ext string, types []string) string {
	switch {
	case acquisition != "acquired":
		return "acquisition is " + acquisition + "; automatic ingest is held"
	case completeness != "verified":
		return "completeness is " + completeness + "; automatic ingest is held"
	case !oneOf(ext, types):
		return fmt.Sprintf("type %q is not a hand-off type (%s)", ext, strings.Join(types, ", "))
	}
	return ""
}

func extensionOf(name string) string {
	return strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
}

func checkTypes(types []string) error {
	if len(types) == 0 {
		return fmt.Errorf("%w: no hand-off types", ErrInvalid)
	}
	for _, t := range types {
		if !oneOf(t, HandoffExtensions) {
			return fmt.Errorf("%w: hand-off type %q (allowed: %s)", ErrInvalid, t, strings.Join(HandoffExtensions, ", "))
		}
	}
	return nil
}

// Handoff writes a manifest for every eligible source capture and reports
// the held ones. types are the extensions the ingest host's backend
// digests. The first capture of an object that is handed off fixes the
// object's extension; later captures of the same bytes reuse it whatever
// their own names say. Handoff is idempotent: an existing identical
// manifest is kept, and a missing one is rewritten with the same content.
func (a *Archive) Handoff(ctx context.Context, types []string) ([]HandoffResult, error) {
	if err := checkTypes(types); err != nil {
		return nil, err
	}
	unlock, err := a.lock(false)
	if err != nil {
		return nil, err
	}
	defer unlock()
	rows, err := a.db.QueryContext(ctx, `SELECT c.capture_id, c.object_id, o.size, c.original_name, c.acquisition, c.completeness FROM captures c JOIN objects o ON o.object_id = c.object_id WHERE c.kind = 'source' AND o.state = 'active' ORDER BY c.recorded_at, c.capture_id`)
	if err != nil {
		return nil, err
	}
	type cand struct {
		m                               Manifest
		acquisition, completeness, name string
	}
	var cands []cand
	for rows.Next() {
		var c cand
		if err := rows.Scan(&c.m.CaptureID, &c.m.ObjectID, &c.m.Size, &c.name, &c.acquisition, &c.completeness); err != nil {
			rows.Close()
			return nil, err
		}
		cands = append(cands, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Captures whose own name has a hand-off type go first, so they fix an
	// object's extension before a same-bytes capture with another name is
	// considered; the result does not depend on recording order.
	sort.SliceStable(cands, func(i, j int) bool {
		return oneOf(extensionOf(cands[i].name), types) && !oneOf(extensionOf(cands[j].name), types)
	})
	out := []HandoffResult{}
	for _, c := range cands {
		r := HandoffResult{CaptureID: c.m.CaptureID, ObjectID: c.m.ObjectID}
		var canonical string
		err := a.db.QueryRowContext(ctx, `SELECT extension FROM handoffs WHERE object_id = ? ORDER BY written_at, capture_id LIMIT 1`, c.m.ObjectID).Scan(&canonical)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		ext := canonical
		if ext == "" {
			ext = extensionOf(c.name)
		}
		if r.Reason = heldReason(c.acquisition, c.completeness, ext, types); r.Reason != "" {
			r.Status = "held"
			out = append(out, r)
			continue
		}
		c.m.Version, c.m.Extension = manifestVersion, ext
		body, err := json.Marshal(c.m)
		if err != nil {
			return nil, err
		}
		dir := filepath.Join(a.root, "outbox")
		written, err := writeOnce(dir, c.m.CaptureID+".json", body)
		if err != nil {
			return nil, err
		}
		now := a.now().UTC().Format(time.RFC3339Nano)
		if _, err := a.db.ExecContext(ctx, `INSERT OR IGNORE INTO handoffs (capture_id, object_id, extension, written_at) VALUES (?,?,?,?)`, c.m.CaptureID, c.m.ObjectID, ext, now); err != nil {
			return nil, err
		}
		r.Status, r.Extension = "existing", ext
		if written {
			r.Status = "written"
		}
		out = append(out, r)
	}
	return out, nil
}

// writeOnce publishes body as dir/name without ever replacing a file. An
// existing file with the same bytes is accepted; different bytes are
// ErrConflict. It reports whether it wrote the file.
func writeOnce(dir, name string, body []byte) (bool, error) {
	final := filepath.Join(dir, name)
	if existing, err := readBounded(final, int64(len(body))+1); err == nil {
		if !bytes.Equal(existing, body) {
			return false, fmt.Errorf("%w: %s exists with different content", ErrConflict, final)
		}
		return false, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Link(tmp.Name(), final); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return writeOnce(dir, name, body)
		}
		return false, err
	}
	return true, syncDir(dir)
}

// readBounded reads a regular file without following a final symlink and
// refuses files larger than limit.
func readBounded(p string, limit int64) ([]byte, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrInvalid, p)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%w: %s is larger than %d bytes", ErrInvalid, p, limit)
	}
	return data, nil
}

// decodeStrict decodes exactly one JSON value with no unknown fields and
// nothing after it but whitespace.
func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if err := dec.Decode(new(json.RawMessage)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: data after the JSON value", ErrInvalid)
	}
	return nil
}

// readManifests returns the valid manifests in archiveRoot/outbox in name
// order. Temporary and unrelated files are skipped; a malformed manifest is
// an error, so a bad hand-off is never silently ignored.
func readManifests(archiveRoot string) ([]Manifest, error) {
	dir := filepath.Join(archiveRoot, "outbox")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("archive outbox: %w", err)
	}
	var out []Manifest
	extOf := map[string]string{}
	for _, e := range entries {
		m := manifestNamePattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		data, err := readBounded(filepath.Join(dir, e.Name()), maxManifestSize)
		if err != nil {
			return nil, err
		}
		var man Manifest
		if err := decodeStrict(data, &man); err != nil {
			return nil, fmt.Errorf("manifest %s: %w", e.Name(), err)
		}
		if man.Version != manifestVersion || man.CaptureID != m[1] || !objectIDPattern.MatchString(man.ObjectID) ||
			!oneOf(man.Extension, HandoffExtensions) || man.Size < 0 || man.Size > MaxObjectBytes {
			return nil, fmt.Errorf("%w: manifest %s has invalid fields", ErrInvalid, e.Name())
		}
		if prev, ok := extOf[man.ObjectID]; ok && prev != man.Extension {
			return nil, fmt.Errorf("%w: object %s is handed off as both .%s and .%s; one object must have one source path", ErrConflict, man.ObjectID, prev, man.Extension)
		}
		extOf[man.ObjectID] = man.Extension
		out = append(out, man)
	}
	return out, nil
}

func checkAbsDir(p, what string) error {
	if !filepath.IsAbs(p) {
		return fmt.Errorf("%s %q: must be absolute", what, p)
	}
	info, err := os.Lstat(p)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s %s: must be a directory, not a symlink or file", what, p)
	}
	return nil
}

// PickupResult is one manifest copied (or found already copied) into the
// ingest host's source directory.
type PickupResult struct {
	CaptureID  string `json:"capture_id"`
	ObjectID   string `json:"object_id"`
	SourcePath string `json:"source_path"`
	// Status is copied, existing, or refused.
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// SourcePath is where the ingest host keeps the copy of an object: named by
// its hash, so identical bytes become one source path and one ledger key.
func SourcePath(destDir string, m Manifest) string {
	return filepath.Join(destDir, strings.TrimPrefix(m.ObjectID, "sha256:")+"."+m.Extension)
}

// Pickup runs on the ingest host. It copies each manifest's object from
// archiveRoot (a file share; the registry is never opened) into destDir,
// verifying size and SHA-256. accept lists the extensions this host's
// ingest digests from destDir; other manifests are refused, not copied. It
// never replaces a file: an existing copy with the right hash is kept, and
// any other existing file is ErrIntegrity.
func Pickup(archiveRoot, destDir string, accept []string) ([]PickupResult, error) {
	if err := checkAbsDir(archiveRoot, "archive root"); err != nil {
		return nil, err
	}
	if err := checkAbsDir(destDir, "pickup destination"); err != nil {
		return nil, err
	}
	if err := checkTypes(accept); err != nil {
		return nil, err
	}
	mans, err := readManifests(archiveRoot)
	if err != nil {
		return nil, err
	}
	out := []PickupResult{}
	for _, m := range mans {
		hexSum := strings.TrimPrefix(m.ObjectID, "sha256:")
		dst := SourcePath(destDir, m)
		r := PickupResult{CaptureID: m.CaptureID, ObjectID: m.ObjectID, SourcePath: dst, Status: "existing"}
		if !oneOf(m.Extension, accept) {
			r.Status, r.SourcePath = "refused", ""
			r.Reason = fmt.Sprintf("this host does not ingest .%s from the destination (accepts %s)", m.Extension, strings.Join(accept, ", "))
			out = append(out, r)
			continue
		}
		if err := verifyFile(dst, hexSum, m.Size); err == nil {
			out = append(out, r)
			continue
		} else if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		if err := copyVerified(filepath.Join(archiveRoot, "objects", "sha256", hexSum), dst, hexSum, m.Size); err != nil {
			return nil, err
		}
		r.Status = "copied"
		out = append(out, r)
	}
	if err := updateCaptureMap(destDir, out); err != nil {
		return out, fmt.Errorf("objects copied, but the capture map was not updated; run pickup again: %w", err)
	}
	return out, nil
}

// openRegular opens p for reading without following a final symlink and
// without blocking: O_NONBLOCK makes a FIFO open return at once, and the
// file is refused unless it is a regular file. A missing file returns an
// fs.ErrNotExist error; anything else that is not a regular file is
// ErrIntegrity. O_NONBLOCK does not change reads of a regular file.
func openRegular(p string) (*os.File, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrIntegrity, p, err)
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("%w: %s is not a regular file", ErrIntegrity, p)
	}
	return f, nil
}

// verifyFile checks p (not following a final symlink) against hexSum and
// size. A missing file returns an fs.ErrNotExist error; anything else that
// does not match is ErrIntegrity.
func verifyFile(p, hexSum string, size int64) error {
	f, err := openRegular(p)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() != size {
		return fmt.Errorf("%w: %s", ErrIntegrity, p)
	}
	h := sha256.New()
	// Read at most one byte past the registered size, so a file that keeps
	// growing cannot keep the check reading.
	n, err := io.Copy(h, io.LimitReader(f, size+1))
	if err != nil {
		return err
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != hexSum {
		return fmt.Errorf("%w: %s", ErrIntegrity, p)
	}
	return nil
}

// copyVerified copies src to dst through a hidden temporary file in dst's
// directory, checking size and hash before it links the copy into place.
func copyVerified(src, dst, hexSum string, size int64) error {
	in, err := openRegular(src)
	if err != nil {
		return err
	}
	defer in.Close()
	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, ".cogvault-pickup-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(in, size+1))
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != hexSum {
		return fmt.Errorf("%w: %s", ErrIntegrity, src)
	}
	if err := os.Chmod(tmp.Name(), 0o444); err != nil {
		return err
	}
	if err := os.Link(tmp.Name(), dst); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return verifyFile(dst, hexSum, size)
		}
		return err
	}
	return syncDir(dir)
}

// LedgerEntry is what the ingest host's ledger says about one source file.
type LedgerEntry struct {
	Status     string
	WikiPage   string
	DigestedAt string
}

// LedgerLookup reads the ingest ledger by its key (source path, content
// hash). found is false when the ledger has no row.
type LedgerLookup func(sourcePath, contentHash string) (entry LedgerEntry, found bool, err error)

// Receipt copies what the ingest host's ledger said at ObservedAt. The
// ledger stays the authority; a receipt is an observation of it.
type Receipt struct {
	Version      int    `json:"version"`
	CaptureID    string `json:"capture_id"`
	ObjectID     string `json:"object_id"`
	SourcePath   string `json:"source_path"`
	ContentHash  string `json:"content_hash"`
	LedgerStatus string `json:"ledger_status"`
	WikiPage     string `json:"wiki_page"`
	DigestedAt   string `json:"digested_at"`
	ObservedAt   string `json:"observed_at"`
}

func (r Receipt) sameObservation(o Receipt) bool {
	return r.SourcePath == o.SourcePath && r.LedgerStatus == o.LedgerStatus && r.WikiPage == o.WikiPage && r.DigestedAt == o.DigestedAt
}

// WriteReceipts runs on the ingest host after ingest. For every manifest
// whose copy exists in destDir and has a ledger row, it writes
// receipts/<capture_id>.<n>.json when the observation differs from the
// latest receipt for that capture. Without a ledger row it writes nothing,
// so nothing claims ingest before the ledger does.
func WriteReceipts(archiveRoot, destDir string, lookup LedgerLookup, now time.Time) ([]Receipt, error) {
	if err := checkAbsDir(archiveRoot, "archive root"); err != nil {
		return nil, err
	}
	if err := checkAbsDir(destDir, "pickup destination"); err != nil {
		return nil, err
	}
	mans, err := readManifests(archiveRoot)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(archiveRoot, "receipts")
	out := []Receipt{}
	for _, m := range mans {
		hexSum := strings.TrimPrefix(m.ObjectID, "sha256:")
		src := SourcePath(destDir, m)
		if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		entry, found, err := lookup(src, hexSum)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		r := Receipt{
			Version: receiptVersion, CaptureID: m.CaptureID, ObjectID: m.ObjectID, SourcePath: src,
			ContentHash: hexSum, LedgerStatus: entry.Status, WikiPage: entry.WikiPage,
			DigestedAt: entry.DigestedAt, ObservedAt: now.UTC().Format(time.RFC3339Nano),
		}
		latest, seq, err := latestReceipt(dir, m.CaptureID)
		if err != nil {
			return nil, err
		}
		if seq > 0 && latest.sameObservation(r) {
			continue
		}
		body, err := json.Marshal(r)
		if err != nil {
			return nil, err
		}
		if _, err := writeOnce(dir, fmt.Sprintf("%s.%d.json", m.CaptureID, seq+1), body); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func latestReceipt(dir, captureID string) (Receipt, int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Receipt{}, 0, err
	}
	best := 0
	for _, e := range entries {
		m := receiptNamePattern.FindStringSubmatch(e.Name())
		if m == nil || m[1] != captureID {
			continue
		}
		if n, _ := strconv.Atoi(m[2]); n > best {
			best = n
		}
	}
	if best == 0 {
		return Receipt{}, 0, nil
	}
	data, err := readBounded(filepath.Join(dir, fmt.Sprintf("%s.%d.json", captureID, best)), maxReceiptSize)
	if err != nil {
		return Receipt{}, 0, err
	}
	var r Receipt
	if err := decodeStrict(data, &r); err != nil {
		return Receipt{}, 0, err
	}
	return r, best, nil
}

// ValidWikiPage reports whether p is a wiki-root-relative .md path, the
// form research_evidence refs use (SPEC §11.1).
func ValidWikiPage(p string) bool {
	return p != "" && filepath.IsLocal(p) && filepath.Clean(p) == p && strings.HasSuffix(p, ".md") && !strings.Contains(p, `\`)
}

// CollectReceipts runs on the archive host. It appends each new receipt to
// the registry unchanged. A receipt already collected must keep its bytes;
// a changed one is ErrConflict. Receipts that do not match a hand-off are
// ErrInvalid.
func (a *Archive) CollectReceipts(ctx context.Context) (int, error) {
	unlock, err := a.lock(false)
	if err != nil {
		return 0, err
	}
	defer unlock()
	dir := filepath.Join(a.root, "receipts")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	// Collect each capture's receipts in sequence order (.2 before .10).
	sort.Slice(entries, func(i, j int) bool { return receiptLess(entries[i].Name(), entries[j].Name()) })
	n := 0
	for _, e := range entries {
		m := receiptNamePattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		data, err := readBounded(filepath.Join(dir, e.Name()), maxReceiptSize)
		if err != nil {
			return n, err
		}
		var prior string
		err = a.db.QueryRowContext(ctx, `SELECT body FROM receipts WHERE name = ?`, e.Name()).Scan(&prior)
		if err == nil {
			if prior != string(data) {
				return n, fmt.Errorf("%w: receipt %s changed after collection", ErrConflict, e.Name())
			}
			continue
		} else if !errors.Is(err, sql.ErrNoRows) {
			return n, err
		}
		var r Receipt
		if err := decodeStrict(data, &r); err != nil {
			return n, fmt.Errorf("receipt %s: %w", e.Name(), err)
		}
		if err := a.checkReceipt(ctx, m[1], r); err != nil {
			return n, fmt.Errorf("receipt %s: %w", e.Name(), err)
		}
		now := a.now().UTC().Format(time.RFC3339Nano)
		tx, err := a.db.BeginTx(ctx, nil)
		if err != nil {
			return n, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO receipts (name, capture_id, observed_at, body, collected_at) VALUES (?,?,?,?,?)`, e.Name(), r.CaptureID, r.ObservedAt, string(data), now); err != nil {
			tx.Rollback()
			return n, err
		}
		if err := linkReceipt(ctx, tx, e.Name(), r, now); err != nil {
			tx.Rollback()
			return n, fmt.Errorf("receipt %s: %w", e.Name(), err)
		}
		if err := tx.Commit(); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func (a *Archive) checkReceipt(ctx context.Context, captureID string, r Receipt) error {
	if r.Version != receiptVersion || r.CaptureID != captureID {
		return fmt.Errorf("%w: version or capture_id does not match the file name", ErrInvalid)
	}
	var objectID string
	err := a.db.QueryRowContext(ctx, `SELECT object_id FROM handoffs WHERE capture_id = ?`, captureID).Scan(&objectID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s was never handed off", ErrInvalid, captureID)
	}
	if err != nil {
		return err
	}
	if r.ObjectID != objectID || r.ContentHash != strings.TrimPrefix(objectID, "sha256:") {
		return fmt.Errorf("%w: object or content hash does not match the hand-off", ErrInvalid)
	}
	if r.LedgerStatus == "" || !filepath.IsAbs(r.SourcePath) {
		return fmt.Errorf("%w: ledger_status and an absolute source_path are required", ErrInvalid)
	}
	if r.WikiPage != "" && !ValidWikiPage(r.WikiPage) {
		return fmt.Errorf("%w: wiki_page %q is not a wiki-relative .md path", ErrInvalid, r.WikiPage)
	}
	if _, err := time.Parse(time.RFC3339Nano, r.ObservedAt); err != nil {
		return fmt.Errorf("%w: observed_at: %v", ErrInvalid, err)
	}
	return checkText(r.SourcePath, r.LedgerStatus, r.WikiPage, r.DigestedAt)
}

// IngestView is the ingest state a status view shows for one capture. It
// never claims a result of its own: observed values come from the latest
// receipt and are labeled with their source and observation time.
type IngestView struct {
	// State is not_applicable, held, not_handed_off, awaiting_ingest, or
	// observed.
	State        string `json:"state"`
	Reason       string `json:"reason,omitempty"`
	Source       string `json:"source,omitempty"`
	ObservedAt   string `json:"observed_at,omitempty"`
	LedgerStatus string `json:"ledger_status,omitempty"`
	SourcePath   string `json:"source_path,omitempty"`
	WikiPage     string `json:"wiki_page,omitempty"`
	// EvidenceRef is a candidate ref: the observed page when the ledger
	// status is success. The archive does not check that the page exists;
	// MCP archive_status keeps it only while the page exists (SPEC §9.16).
	EvidenceRef string `json:"evidence_ref,omitempty"`
	// RecoveredAs lists the captures recorded as recovering a held capture
	// (attempts with a result capture). The held capture itself stays held;
	// its ingest goes through those captures.
	RecoveredAs []string `json:"recovered_as,omitempty"`
}

func (a *Archive) ingestView(ctx context.Context, c Capture) (IngestView, error) {
	if c.Kind != "source" {
		return IngestView{State: "not_applicable"}, nil
	}
	r, ok, err := a.latestObservation(ctx, c.CaptureID)
	if err != nil {
		return IngestView{}, err
	}
	if ok {
		v := IngestView{State: "observed", Source: "ingest_host_receipt", ObservedAt: r.ObservedAt, LedgerStatus: r.LedgerStatus, SourcePath: r.SourcePath, WikiPage: r.WikiPage}
		if r.LedgerStatus == "success" && r.WikiPage != "" {
			v.EvidenceRef = r.WikiPage
		}
		return v, nil
	}
	var handed int
	if err := a.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM handoffs WHERE capture_id = ?`, c.CaptureID).Scan(&handed); err != nil {
		return IngestView{}, err
	}
	if handed > 0 {
		return IngestView{State: "awaiting_ingest", Reason: "no receipt yet; this is not a success or failure claim"}, nil
	}
	if reason := heldReason(c.Acquisition, c.Completeness, extensionOf(c.OriginalName), HandoffExtensions); reason != "" {
		v := IngestView{State: "held", Reason: reason}
		for _, at := range c.Attempts {
			if at.ResultCaptureID != "" {
				v.RecoveredAs = append(v.RecoveredAs, at.ResultCaptureID)
			}
		}
		return v, nil
	}
	return IngestView{State: "not_handed_off"}, nil
}
