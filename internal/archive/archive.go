// Package archive is the local prototype of the research source archive
// proposed in docs/decisions/0027 (proposed, not accepted). It stores
// original bytes as immutable content-addressed objects and keeps provenance,
// completeness, and report citations in a separate SQLite registry under one
// root directory. Provenance records what was supplied; it is not a fact check.
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
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

// MaxObjectBytes bounds one stored object. Larger input is rejected, never
// truncated.
const MaxObjectBytes = 32 << 20

// managedDirs are created at Open and must be real directories, not
// symlinks. outbox/ and receipts/ are the hand-off files (ADR 0027 item 9);
// quarantine/ and deletions/ hold quarantined objects and the durable
// deletion records (item 11).
var managedDirs = []string{
	"objects", filepath.Join("objects", "sha256"), filepath.Join("objects", "tmp"),
	"outbox", "receipts", "quarantine", "deletions",
}

const (
	schemaVersion = 5
	maxFieldRunes = 1000
	maxKeyRunes   = 200
	autoKeyPrefix = "digest:"
)

var (
	ErrInvalid   = errors.New("invalid archive request")
	ErrConflict  = errors.New("request key already used with different content")
	ErrNotFound  = errors.New("capture not found")
	ErrTooLarge  = fmt.Errorf("object exceeds %d bytes", MaxObjectBytes)
	ErrIntegrity = errors.New("stored object does not match its hash")
	// ErrDeleted reports an object that is quarantined or purged. Its bytes
	// cannot be stored again until an operator restores it, so a new capture
	// never brings back an original the user asked to delete.
	ErrDeleted = errors.New("object is quarantined or purged")
)

const capturesColumns = `
 capture_id TEXT PRIMARY KEY,
 request_key TEXT NOT NULL UNIQUE,
 request_digest TEXT NOT NULL,
 kind TEXT NOT NULL CHECK (kind IN ('source','report','summary')),
 object_id TEXT NOT NULL REFERENCES objects(object_id),
 media_type TEXT NOT NULL,
 original_name TEXT NOT NULL,
 url TEXT NOT NULL,
 supplier TEXT NOT NULL,
 title TEXT NOT NULL,
 acquisition TEXT NOT NULL CHECK (acquisition IN ('acquired','partial','not_applicable')),
 completeness TEXT NOT NULL CHECK (completeness IN ('verified','unknown','incomplete','not_applicable')),
 completeness_basis TEXT NOT NULL,
 recorded_at TEXT NOT NULL,
 summary_of TEXT REFERENCES captures(capture_id),
 CHECK (summary_of IS NULL OR kind = 'summary'),
 CHECK (kind <> 'summary' OR summary_of IS NOT NULL OR url <> '')`

// v2Tables are the tables added in schema version 2. They are append-only
// except handoffs, which records one manifest per capture.
const v2Tables = `
CREATE TABLE IF NOT EXISTS attempts (
 attempt_id INTEGER PRIMARY KEY AUTOINCREMENT,
 capture_id TEXT NOT NULL REFERENCES captures(capture_id),
 cause TEXT NOT NULL CHECK (cause IN ('citation_only','truncated','access_restricted','missing_parts','edition_mismatch','extraction_failed','other')),
 tried TEXT NOT NULL,
 result TEXT NOT NULL CHECK (result IN ('acquired','partial','failed','human_action_required')),
 result_capture_id TEXT REFERENCES captures(capture_id),
 remaining_gap TEXT NOT NULL,
 next_action TEXT NOT NULL,
 recorded_at TEXT NOT NULL,
 CHECK ((result IN ('acquired','partial')) = (result_capture_id IS NOT NULL)));
CREATE INDEX IF NOT EXISTS attempts_capture ON attempts(capture_id);
CREATE TABLE IF NOT EXISTS handoffs (
 capture_id TEXT PRIMARY KEY REFERENCES captures(capture_id),
 object_id TEXT NOT NULL REFERENCES objects(object_id),
 extension TEXT NOT NULL,
 written_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS receipts (
 name TEXT PRIMARY KEY,
 capture_id TEXT NOT NULL REFERENCES captures(capture_id),
 observed_at TEXT NOT NULL,
 body TEXT NOT NULL,
 collected_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS receipts_capture ON receipts(capture_id);
CREATE TABLE IF NOT EXISTS derivations (
 derived_object_id TEXT NOT NULL REFERENCES objects(object_id),
 derived_name TEXT NOT NULL,
 kind TEXT NOT NULL CHECK (kind IN ('cogvault-fetch-md')),
 capture_id TEXT NOT NULL REFERENCES captures(capture_id),
 acquired_at TEXT NOT NULL,
 recorded_at TEXT NOT NULL,
 PRIMARY KEY (derived_object_id, capture_id));
CREATE INDEX IF NOT EXISTS derivations_capture ON derivations(capture_id);
CREATE TABLE IF NOT EXISTS acquisitions (
 capture_id TEXT PRIMARY KEY REFERENCES captures(capture_id),
 record TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS extractions (
 source_object_id TEXT NOT NULL REFERENCES objects(object_id),
 extractor TEXT NOT NULL,
 status TEXT NOT NULL CHECK (status IN ('succeeded','failed')),
 output_object_id TEXT REFERENCES objects(object_id),
 detail TEXT NOT NULL,
 recorded_at TEXT NOT NULL,
 PRIMARY KEY (source_object_id, extractor),
 CHECK ((status = 'succeeded') = (output_object_id IS NOT NULL)));
`

const ddl = `
CREATE TABLE IF NOT EXISTS objects (
 object_id TEXT PRIMARY KEY,
 size INTEGER NOT NULL,
 stored_at TEXT NOT NULL,
 state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active','quarantined','purged')),
 state_at TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS captures (` + capturesColumns + `);
CREATE INDEX IF NOT EXISTS captures_object ON captures(object_id);
CREATE TABLE IF NOT EXISTS citations (
 report_id TEXT NOT NULL REFERENCES captures(capture_id),
 position INTEGER NOT NULL,
 cited_capture_id TEXT REFERENCES captures(capture_id),
 cited_url TEXT NOT NULL,
 PRIMARY KEY (report_id, position),
 CHECK ((cited_capture_id IS NULL) <> (cited_url = '')));
CREATE INDEX IF NOT EXISTS citations_cited ON citations(cited_capture_id);
` + v2Tables + v3Tables + linkOnceIndex

// linkOnceIndex (schema version 5) allows one linking attempt per (held
// capture, result capture) pair, so concurrent reassessments of the same
// capture link it once.
const linkOnceIndex = `
CREATE UNIQUE INDEX IF NOT EXISTS attempts_link_once ON attempts(capture_id, result_capture_id) WHERE result_capture_id IS NOT NULL;
`

// Archive is one archive root: objects/sha256/<hex> plus registry.db.
type Archive struct {
	root string
	db   *sql.DB
	now  func() time.Time
}

// SourceRequest records one manually supplied source original.
type SourceRequest struct {
	// Key is an optional idempotency key. When empty, the request digest is
	// the key, so an identical request returns the existing capture.
	Key          string
	Content      io.Reader
	OriginalName string
	URL          string
	Supplier     string
	Title        string
	MediaType    string
	// Completeness is verified, unknown (default), or incomplete. verified
	// requires Basis: the checkable evidence for the judgment.
	Completeness string
	Basis        string
}

// ReportRequest records a research report and the sources it cites, in
// report order. A cite is a source capture ID or an http(s) URL; a URL cite
// does not create a capture or claim that the original was acquired.
type ReportRequest struct {
	Key          string
	Content      io.Reader
	OriginalName string
	Supplier     string
	Title        string
	Cites        []string
}

type Result struct {
	CaptureID string `json:"capture_id"`
	ObjectID  string `json:"object_id"`
	Existing  bool   `json:"existing"`
}

type Capture struct {
	CaptureID         string     `json:"capture_id"`
	Kind              string     `json:"kind"`
	ObjectID          string     `json:"object_id"`
	Size              int64      `json:"size"`
	MediaType         string     `json:"media_type"`
	OriginalName      string     `json:"original_name"`
	URL               string     `json:"url"`
	Supplier          string     `json:"supplier"`
	Title             string     `json:"title"`
	Acquisition       string     `json:"acquisition"`
	Completeness      string     `json:"completeness"`
	CompletenessBasis string     `json:"completeness_basis"`
	RecordedAt        string     `json:"recorded_at"`
	SummaryOf         string     `json:"summary_of,omitempty"`
	SameObject        []string   `json:"same_object"`
	CitedBy           []string   `json:"cited_by"`
	Citations         []Citation `json:"citations"`
	Summaries         []string   `json:"summaries"`
	// Attempts are recovery attempts for this capture; RecoveredBy are the
	// attempts that produced it.
	Attempts    []Attempt  `json:"attempts"`
	RecoveredBy []Attempt  `json:"recovered_by"`
	Ingest      IngestView `json:"ingest"`
	// Derived lists processed files known to be made from this capture's
	// bytes (for example a cogvault fetch .md). They are not originals.
	Derived []Derivation `json:"derived"`
	// Download is the response provenance of an Acquire download.
	Download *AcquisitionRecord `json:"download,omitempty"`
	// Extractions are text extractions of this capture's bytes.
	Extractions []Extraction `json:"extractions"`
	// PageLinks are the archive's capture-to-page associations (pagelinks.go).
	PageLinks []PageLink `json:"page_links"`
}

// Citation is one report citation. Exactly one of CaptureID and URL is set;
// Completeness is the cited source capture's current completeness.
type Citation struct {
	Position     int    `json:"position"`
	CaptureID    string `json:"capture_id,omitempty"`
	URL          string `json:"url,omitempty"`
	Completeness string `json:"completeness,omitempty"`
}

// Open opens or creates the archive at an absolute root. Open refuses a root
// that is itself a symlink, and refuses managed entries (objects/ and its
// subdirectories, registry.db and its -wal, -shm, and -journal files) that
// already exist as symlinks or other non-regular entries. Symlinks in the
// root's ancestors are trusted operator input. These checks run at Open; a
// same-user process that swaps entries inside the root while the archive is
// open is outside this guarantee (as in docs/decisions/0004).
func Open(root string) (*Archive, error) {
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("archive root %q: must be absolute", root)
	}
	root = filepath.Clean(root)
	if info, err := os.Lstat(root); err == nil && info.Mode()&fs.ModeSymlink != 0 {
		return nil, fmt.Errorf("archive root %s: must not be a symlink; pass the real directory", root)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("archive root: %w", err)
	}
	if err := ensureDir(root); err != nil {
		return nil, err
	}
	for _, dir := range managedDirs {
		if err := ensureDir(filepath.Join(root, dir)); err != nil {
			return nil, err
		}
	}
	dbPath := filepath.Join(root, "registry.db")
	for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
		p := dbPath + suffix
		if info, err := os.Lstat(p); err == nil && !info.Mode().IsRegular() {
			return nil, fmt.Errorf("archive registry %s: not a regular file", p)
		} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("archive registry: %w", err)
		}
	}
	// _txlock=immediate: every transaction takes the write lock at BEGIN, so
	// two handles (or processes) writing under the shared archive lock wait
	// on busy_timeout instead of failing with SQLITE_BUSY_SNAPSHOT when a
	// read transaction tries to upgrade.
	db, err := sql.Open("sqlite", dbPath+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_txlock=immediate")
	if err != nil {
		return nil, fmt.Errorf("archive registry: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("archive registry: %w", err)
	}
	a := &Archive{root: root, db: db, now: time.Now}
	unlock, err := a.lock(context.Background(), true)
	if err != nil {
		db.Close()
		return nil, err
	}
	_, err = a.applyDeletions(context.Background())
	unlock()
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("archive deletion log: %w", err)
	}
	return a, nil
}

// lock takes the archive lock (flock on root/archive.lock) for one
// operation. Writes that add data (captures, attempts, hand-offs,
// receipts) share it; Backup, deletion actions, and Status hold it
// exclusively, so a backup never sees a registry snapshot and an object set
// from different moments, deletion records get one total order, and a
// status is one point in time. Each call opens its own descriptor, so the
// lock also orders goroutines of one process and other processes on the
// same root. While the lock is held elsewhere, lock retries a non-blocking
// flock every lockPoll and returns ctx's error once ctx ends, so a
// cancelled request never waits for a stuck holder.
func (a *Archive) lock(ctx context.Context, exclusive bool) (func(), error) {
	p := filepath.Join(a.root, "archive.lock")
	f, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE|noFollow, 0o600)
	if err != nil {
		return nil, fmt.Errorf("archive lock: %w", err)
	}
	how := unix.LOCK_SH
	if exclusive {
		how = unix.LOCK_EX
	}
	for {
		err = unix.Flock(int(f.Fd()), how|unix.LOCK_NB)
		if err == nil {
			break
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			f.Close()
			return nil, fmt.Errorf("archive lock: %w", err)
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, fmt.Errorf("archive lock: %w", ctx.Err())
		case <-time.After(lockPoll):
		}
	}
	return func() {
		unix.Flock(int(f.Fd()), unix.LOCK_UN)
		f.Close()
	}, nil
}

const lockPoll = 5 * time.Millisecond

func ensureDir(p string) error {
	info, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.Mkdir(p, 0o700); err != nil {
			return fmt.Errorf("archive: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("archive: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("archive: %s must be a directory, not a symlink or file", p)
	}
	return nil
}

func migrate(db *sql.DB) error {
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return err
	}
	switch v {
	case 0, schemaVersion:
		if _, err := db.Exec(ddl); err != nil {
			return err
		}
		_, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion))
		return err
	case 1:
		return migrateV1(db)
	case 2, 3, 4:
		return migrateLinks(db, v)
	default:
		return fmt.Errorf("unsupported registry schema version %d", v)
	}
}

// migrateLinks upgrades a version 2, 3, or 4 registry in one transaction.
// It creates page_links if missing (v2) and relinks every collected receipt,
// which repairs links recorded before the receipt-order contract (a version
// 3 registry could keep a .10 basis over an already collected .2, because
// collection sorted names): linkReceipt keeps the lowest-numbered linking
// receipt as the basis. It then adds linkOnceIndex; a registry that already
// holds a duplicate link fails here with the constraint error and is left
// unchanged.
func migrateLinks(db *sql.DB, from int) error {
	ctx := context.Background()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, v3Tables); err != nil {
		return fmt.Errorf("migrate registry v%d: %w", from, err)
	}
	if err := backfillPageLinks(ctx, tx); err != nil {
		return fmt.Errorf("migrate registry v%d: %w", from, err)
	}
	if _, err := tx.ExecContext(ctx, linkOnceIndex); err != nil {
		return fmt.Errorf("migrate registry v%d: %w", from, err)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

// migrateV1 rebuilds captures for the summary kind and the not_applicable
// states, following SQLite's documented table-rebuild procedure: foreign
// keys off on one pinned connection, rebuild in one transaction, check, on.
func migrateV1(db *sql.DB) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
		return err
	}
	defer conn.ExecContext(ctx, `PRAGMA foreign_keys = ON`)
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	steps := []string{
		`CREATE TABLE captures_v2 (` + capturesColumns + `)`,
		`INSERT INTO captures_v2 SELECT capture_id, request_key, request_digest, kind, object_id, media_type, original_name, url, supplier, title, acquisition, completeness, completeness_basis, recorded_at, NULL FROM captures`,
		`DROP TABLE captures`,
		`ALTER TABLE captures_v2 RENAME TO captures`,
		`CREATE INDEX IF NOT EXISTS captures_object ON captures(object_id)`,
		`ALTER TABLE objects ADD COLUMN state TEXT NOT NULL DEFAULT 'active' CHECK (state IN ('active','quarantined','purged'))`,
		`ALTER TABLE objects ADD COLUMN state_at TEXT NOT NULL DEFAULT ''`,
		v2Tables,
		v3Tables,
		linkOnceIndex,
	}
	for _, s := range steps {
		if _, err := tx.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("migrate registry v1: %w", err)
		}
	}
	var bad int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&bad); err != nil {
		return err
	}
	if bad != 0 {
		return fmt.Errorf("migrate registry v1: %d foreign key violations", bad)
	}
	if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *Archive) Close() error { return a.db.Close() }

// CaptureSource stores the original bytes and records their provenance.
func (a *Archive) CaptureSource(ctx context.Context, req SourceRequest) (Result, error) {
	return a.captureSource(ctx, req, "")
}

// captureSource is CaptureSource with an optional acquisition time that
// becomes part of the request identity.
func (a *Archive) captureSource(ctx context.Context, req SourceRequest, acquiredAt string) (Result, error) {
	completeness := req.Completeness
	if completeness == "" {
		completeness = "unknown"
	}
	switch completeness {
	case "unknown", "incomplete":
	case "verified":
		if strings.TrimSpace(req.Basis) == "" {
			return Result{}, fmt.Errorf("%w: verified completeness requires a basis", ErrInvalid)
		}
	default:
		return Result{}, fmt.Errorf("%w: completeness %q (want verified, unknown, or incomplete)", ErrInvalid, completeness)
	}
	if err := checkURL(req.URL, true); err != nil {
		return Result{}, err
	}
	if err := checkKey(req.Key); err != nil {
		return Result{}, err
	}
	if err := checkText(req.OriginalName, req.Supplier, req.Title, req.MediaType, req.Basis); err != nil {
		return Result{}, err
	}
	acquisition := "acquired"
	if completeness == "incomplete" {
		acquisition = "partial"
	}
	rec := record{
		Kind: "source", OriginalName: req.OriginalName, URL: req.URL, Supplier: req.Supplier,
		Title: req.Title, MediaType: req.MediaType, Acquisition: acquisition,
		Completeness: completeness, Basis: req.Basis, AcquiredAt: acquiredAt,
	}
	return a.store(ctx, req.Key, req.Content, rec)
}

// ImportReport stores the report bytes and its ordered citations.
func (a *Archive) ImportReport(ctx context.Context, req ReportRequest) (Result, error) {
	if err := checkKey(req.Key); err != nil {
		return Result{}, err
	}
	if err := checkText(req.OriginalName, req.Supplier, req.Title); err != nil {
		return Result{}, err
	}
	for _, cite := range req.Cites {
		if !strings.HasPrefix(cite, "cap_") {
			if err := checkURL(cite, false); err != nil {
				return Result{}, err
			}
		}
	}
	rec := record{
		Kind: "report", OriginalName: req.OriginalName, Supplier: req.Supplier, Title: req.Title,
		Acquisition: "acquired", Completeness: "unknown", Cites: req.Cites,
	}
	return a.store(ctx, req.Key, req.Content, rec)
}

// SummaryRequest records a finished summary of a source. A summary is never
// an original: it has no acquisition or completeness of its own and never
// changes the summarized capture. SummaryOf (a source capture ID), URL, or
// both name what was summarized.
type SummaryRequest struct {
	Key          string
	Content      io.Reader
	OriginalName string
	SummaryOf    string
	URL          string
	Supplier     string
	Title        string
}

// CaptureSummary stores a summary as its own capture of kind summary.
func (a *Archive) CaptureSummary(ctx context.Context, req SummaryRequest) (Result, error) {
	if req.SummaryOf == "" && req.URL == "" {
		return Result{}, fmt.Errorf("%w: a summary needs the summarized capture or URL", ErrInvalid)
	}
	if req.SummaryOf != "" && !strings.HasPrefix(req.SummaryOf, "cap_") {
		return Result{}, fmt.Errorf("%w: summary_of %q is not a capture ID", ErrInvalid, req.SummaryOf)
	}
	if err := checkURL(req.URL, true); err != nil {
		return Result{}, err
	}
	if err := checkKey(req.Key); err != nil {
		return Result{}, err
	}
	if err := checkText(req.OriginalName, req.Supplier, req.Title, req.SummaryOf); err != nil {
		return Result{}, err
	}
	rec := record{
		Kind: "summary", OriginalName: req.OriginalName, URL: req.URL, Supplier: req.Supplier,
		Title: req.Title, Acquisition: "not_applicable", Completeness: "not_applicable",
		SummaryOf: req.SummaryOf,
	}
	return a.store(ctx, req.Key, req.Content, rec)
}

// record is the canonical request; its JSON digest identifies a request.
// Field order is fixed by the struct, so the digest is stable. SummaryOf is
// omitted when empty so schema-1 request digests (and IDs) stay unchanged.
type record struct {
	Kind         string   `json:"kind"`
	ObjectID     string   `json:"object_id"`
	OriginalName string   `json:"original_name"`
	URL          string   `json:"url"`
	Supplier     string   `json:"supplier"`
	Title        string   `json:"title"`
	MediaType    string   `json:"media_type"`
	Acquisition  string   `json:"acquisition"`
	Completeness string   `json:"completeness"`
	Basis        string   `json:"basis"`
	Cites        []string `json:"cites"`
	SummaryOf    string   `json:"summary_of,omitempty"`
	// AcquiredAt is set only when the request states when the bytes were
	// acquired (a fetch time); omitted otherwise so older digests hold.
	AcquiredAt string `json:"acquired_at,omitempty"`
}

func (a *Archive) store(ctx context.Context, key string, content io.Reader, rec record) (Result, error) {
	if content == nil {
		return Result{}, fmt.Errorf("%w: content is required", ErrInvalid)
	}
	unlock, err := a.lock(ctx, false)
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	staged, err := a.stage(content)
	if err != nil {
		return Result{}, err
	}
	defer os.Remove(staged.tmpPath)
	rec.ObjectID = staged.objectID
	if rec.MediaType == "" {
		rec.MediaType = staged.sniffed
	}
	if rec.Cites == nil {
		rec.Cites = []string{}
	}
	canonical, err := json.Marshal(rec)
	if err != nil {
		return Result{}, err
	}
	sum := sha256.Sum256(append([]byte("cogvault-archive-request-v1\x00"), canonical...))
	digest := hex.EncodeToString(sum[:])
	if key == "" {
		key = autoKeyPrefix + digest
	}
	idSum := sha256.Sum256([]byte("cogvault-capture-id-v1\x00" + key))
	captureID := "cap_" + hex.EncodeToString(idSum[:16])

	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback()
	var state string
	err = tx.QueryRowContext(ctx, `SELECT state FROM objects WHERE object_id = ?`, staged.objectID).Scan(&state)
	if err == nil && state != "active" {
		return Result{}, fmt.Errorf("%w: %s is %s", ErrDeleted, staged.objectID, state)
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Result{}, err
	}
	var existingID, existingDigest, existingObject string
	err = tx.QueryRowContext(ctx, `SELECT capture_id, request_digest, object_id FROM captures WHERE request_key = ?`, key).Scan(&existingID, &existingDigest, &existingObject)
	switch {
	case err == nil:
		if existingDigest != digest {
			return Result{}, fmt.Errorf("%w: key %q", ErrConflict, key)
		}
		// The request is identical, so its object is the staged one. Verify
		// the stored bytes anyway: a retry must not hide a damaged original.
		if err := a.verifyObject(staged); err != nil {
			return Result{}, err
		}
		return Result{CaptureID: existingID, ObjectID: existingObject, Existing: true}, nil
	case !errors.Is(err, sql.ErrNoRows):
		return Result{}, err
	}
	refs := rec.Cites
	if rec.SummaryOf != "" {
		refs = append([]string{rec.SummaryOf}, refs...)
	}
	for _, ref := range refs {
		if !strings.HasPrefix(ref, "cap_") {
			continue
		}
		if err := activeSource(ctx, tx, ref); err != nil {
			return Result{}, err
		}
	}
	if err := a.publish(staged); err != nil {
		return Result{}, err
	}
	now := a.now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO objects (object_id, size, stored_at) VALUES (?, ?, ?)`, staged.objectID, staged.size, now); err != nil {
		return Result{}, err
	}
	var summaryOf any
	if rec.SummaryOf != "" {
		summaryOf = rec.SummaryOf
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO captures (capture_id, request_key, request_digest, kind, object_id, media_type, original_name, url, supplier, title, acquisition, completeness, completeness_basis, recorded_at, summary_of) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		captureID, key, digest, rec.Kind, rec.ObjectID, rec.MediaType, rec.OriginalName, rec.URL, rec.Supplier, rec.Title, rec.Acquisition, rec.Completeness, rec.Basis, now, summaryOf); err != nil {
		return Result{}, err
	}
	for i, cite := range rec.Cites {
		var capID any
		citedURL := cite
		if strings.HasPrefix(cite, "cap_") {
			capID, citedURL = cite, ""
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO citations (report_id, position, cited_capture_id, cited_url) VALUES (?,?,?,?)`, captureID, i+1, capID, citedURL); err != nil {
			return Result{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Result{}, err
	}
	return Result{CaptureID: captureID, ObjectID: staged.objectID}, nil
}

// activeSource checks that id names a source capture whose object is active.
// A capture of a quarantined or purged object counts as not found: it has
// left every capture tool and status view.
func activeSource(ctx context.Context, q querier, id string) error {
	var kind, state string
	err := q.QueryRowContext(ctx, `SELECT c.kind, o.state FROM captures c JOIN objects o ON o.object_id = c.object_id WHERE c.capture_id = ?`, id).Scan(&kind, &state)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && state != "active") {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return err
	}
	if kind != "source" {
		return fmt.Errorf("%w: %s is a %s, not a source capture", ErrInvalid, id, kind)
	}
	return nil
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type stagedObject struct {
	tmpPath  string
	objectID string
	hexSum   string
	size     int64
	sniffed  string
}

// stage copies content to a temporary file under objects/tmp while hashing
// it. The file is synced and made read-only before publish links it.
func (a *Archive) stage(content io.Reader) (stagedObject, error) {
	tmp, err := os.CreateTemp(filepath.Join(a.root, "objects", "tmp"), "stage-*")
	if err != nil {
		return stagedObject{}, err
	}
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	h := sha256.New()
	head := &prefixBuffer{limit: 512}
	n, err := io.Copy(io.MultiWriter(tmp, h, head), io.LimitReader(content, MaxObjectBytes+1))
	if err != nil {
		return stagedObject{}, err
	}
	if n > MaxObjectBytes {
		return stagedObject{}, ErrTooLarge
	}
	if err := tmp.Sync(); err != nil {
		return stagedObject{}, err
	}
	if err := tmp.Close(); err != nil {
		return stagedObject{}, err
	}
	if err := os.Chmod(tmp.Name(), 0o444); err != nil {
		return stagedObject{}, err
	}
	ok = true
	sum := hex.EncodeToString(h.Sum(nil))
	return stagedObject{tmpPath: tmp.Name(), objectID: "sha256:" + sum, hexSum: sum, size: n, sniffed: http.DetectContentType(head.buf)}, nil
}

// publish hard-links the staged file to its content address. A link never
// replaces an existing file; an existing object is verified instead.
func (a *Archive) publish(s stagedObject) error {
	final := a.objectPath(s.hexSum)
	err := os.Link(s.tmpPath, final)
	if err == nil {
		return syncDir(filepath.Dir(final))
	}
	if !errors.Is(err, fs.ErrExist) {
		return err
	}
	return a.verifyObject(s)
}

func (a *Archive) objectPath(hexSum string) string {
	return filepath.Join(a.root, "objects", "sha256", hexSum)
}

// verifyObject checks that the stored object for s is a regular file whose
// size and SHA-256 match. A missing, replaced, or altered object is
// ErrIntegrity; nothing is rewritten.
func (a *Archive) verifyObject(s stagedObject) error {
	final := a.objectPath(s.hexSum)
	f, err := os.OpenFile(final, os.O_RDONLY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("%w: %s: %v", ErrIntegrity, final, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != s.size {
		return fmt.Errorf("%w: %s", ErrIntegrity, final)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != s.hexSum {
		return fmt.Errorf("%w: %s", ErrIntegrity, final)
	}
	return nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

type prefixBuffer struct {
	buf   []byte
	limit int
}

func (p *prefixBuffer) Write(b []byte) (int, error) {
	if room := p.limit - len(p.buf); room > 0 {
		if len(b) < room {
			room = len(b)
		}
		p.buf = append(p.buf, b[:room]...)
	}
	return len(b), nil
}

func checkURL(raw string, optional bool) error {
	if raw == "" && optional {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%w: %q is not an absolute http(s) URL", ErrInvalid, raw)
	}
	return checkText(raw)
}

func checkKey(key string) error {
	if strings.HasPrefix(key, autoKeyPrefix) {
		return fmt.Errorf("%w: key must not start with %q", ErrInvalid, autoKeyPrefix)
	}
	if !utf8.ValidString(key) || utf8.RuneCountInString(key) > maxKeyRunes {
		return fmt.Errorf("%w: key is not valid UTF-8 or longer than %d characters", ErrInvalid, maxKeyRunes)
	}
	return nil
}

func checkText(fields ...string) error {
	for _, f := range fields {
		if !utf8.ValidString(f) || utf8.RuneCountInString(f) > maxFieldRunes {
			return fmt.Errorf("%w: field is not valid UTF-8 or longer than %d characters", ErrInvalid, maxFieldRunes)
		}
	}
	return nil
}

// Status returns one capture with its relations, recovery history, and
// ingest view. Captures of a quarantined or purged object are not found.
// It holds the archive lock exclusively. Every registry write holds the
// lock (shared to add data, exclusive for deletion actions), and Status
// reads only the registry, so the view is one point in time: no write lands
// between its queries.
func (a *Archive) Status(ctx context.Context, captureID string) (Capture, error) {
	unlock, err := a.lock(ctx, true)
	if err != nil {
		return Capture{}, err
	}
	defer unlock()
	return a.status(ctx, captureID)
}

// status is Status for a caller that already holds the archive lock
// exclusively.
func (a *Archive) status(ctx context.Context, captureID string) (Capture, error) {
	row := a.db.QueryRowContext(ctx, captureSelect+` WHERE c.capture_id = ?`, captureID)
	c, err := scanCapture(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Capture{}, fmt.Errorf("%w: %s", ErrNotFound, captureID)
	}
	if err != nil {
		return Capture{}, err
	}
	if c.SameObject, err = a.ids(ctx, `SELECT capture_id FROM captures WHERE object_id = ? AND capture_id <> ? ORDER BY recorded_at, capture_id`, c.ObjectID, c.CaptureID); err != nil {
		return Capture{}, err
	}
	if c.CitedBy, err = a.ids(ctx, `SELECT DISTINCT t.report_id FROM citations t JOIN captures r ON r.capture_id = t.report_id JOIN objects o ON o.object_id = r.object_id WHERE t.cited_capture_id = ? AND o.state = 'active' ORDER BY t.report_id`, c.CaptureID); err != nil {
		return Capture{}, err
	}
	if c.Summaries, err = a.ids(ctx, `SELECT s.capture_id FROM captures s JOIN objects o ON o.object_id = s.object_id WHERE s.summary_of = ? AND o.state = 'active' ORDER BY s.recorded_at, s.capture_id`, c.CaptureID); err != nil {
		return Capture{}, err
	}
	if c.Attempts, err = a.attempts(ctx, "capture_id", c.CaptureID); err != nil {
		return Capture{}, err
	}
	if c.RecoveredBy, err = a.attempts(ctx, "result_capture_id", c.CaptureID); err != nil {
		return Capture{}, err
	}
	if c.Download, err = a.acquisition(ctx, c.CaptureID); err != nil {
		return Capture{}, err
	}
	if c.Extractions, err = a.extractions(ctx, c.ObjectID); err != nil {
		return Capture{}, err
	}
	if c.Derived, err = a.derivations(ctx, c.CaptureID); err != nil {
		return Capture{}, err
	}
	if c.Ingest, err = a.ingestView(ctx, c); err != nil {
		return Capture{}, err
	}
	if c.PageLinks, err = a.pageLinks(ctx, `l.capture_id = ?`, c.CaptureID); err != nil {
		return Capture{}, err
	}
	rows, err := a.db.QueryContext(ctx, `SELECT t.position, COALESCE(t.cited_capture_id, ''), t.cited_url, COALESCE(CASE WHEN o.state = 'active' THEN s.completeness END, '') FROM citations t LEFT JOIN captures s ON s.capture_id = t.cited_capture_id LEFT JOIN objects o ON o.object_id = s.object_id WHERE t.report_id = ? ORDER BY t.position`, c.CaptureID)
	if err != nil {
		return Capture{}, err
	}
	defer rows.Close()
	c.Citations = []Citation{}
	for rows.Next() {
		var cit Citation
		if err := rows.Scan(&cit.Position, &cit.CaptureID, &cit.URL, &cit.Completeness); err != nil {
			return Capture{}, err
		}
		c.Citations = append(c.Citations, cit)
	}
	return c, rows.Err()
}

// List returns every capture of an active object in recording order,
// without relations.
func (a *Archive) List(ctx context.Context) ([]Capture, error) {
	rows, err := a.db.QueryContext(ctx, captureSelect+` ORDER BY c.recorded_at, c.capture_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Capture{}
	for rows.Next() {
		c, err := scanCapture(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

const captureSelect = `SELECT c.capture_id, c.kind, c.object_id, o.size, c.media_type, c.original_name, c.url, c.supplier, c.title, c.acquisition, c.completeness, c.completeness_basis, c.recorded_at, COALESCE(c.summary_of, '') FROM captures c JOIN objects o ON o.object_id = c.object_id AND o.state = 'active'`

type scanner interface{ Scan(...any) error }

func scanCapture(s scanner) (Capture, error) {
	var c Capture
	err := s.Scan(&c.CaptureID, &c.Kind, &c.ObjectID, &c.Size, &c.MediaType, &c.OriginalName, &c.URL, &c.Supplier, &c.Title, &c.Acquisition, &c.Completeness, &c.CompletenessBasis, &c.RecordedAt, &c.SummaryOf)
	return c, err
}

func (a *Archive) ids(ctx context.Context, query string, args ...any) ([]string, error) {
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
