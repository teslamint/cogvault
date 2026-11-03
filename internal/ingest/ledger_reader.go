package ingest

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// LedgerReader reads the ingest ledger for archive receipts. The ledger is
// the authority for ingest results, so the reader must not change the
// database or create any file beside it.
//
// SQLite's mode=ro alone is not enough: a read-only connection to a WAL
// database still creates the -wal and -shm files. The reader therefore
// takes the existing ingest lock (no ingest can write while it is held),
// refuses a leftover -wal (its rows would be invisible), and opens the file
// with mode=ro&immutable=1, which reads the main file without WAL or
// locking. It never creates ingest.lock: a ledger directory without one has
// never completed an ingest run, and the reader refuses it.
type LedgerReader struct {
	db     *sql.DB
	unlock func()
}

// LedgerEntry is one ledger row as the reader reports it.
type LedgerEntry struct {
	Status     string
	WikiPage   string
	DigestedAt string
}

// OpenLedgerReader opens dbPath read-only. dbPath must be an absolute path
// to an existing regular file (not a symlink) that holds the ingest_ledger
// table, and its directory must hold the ingest.lock that ingest runs
// create; anything else is an error rather than an empty ledger. While the
// reader is open, ingest cannot start (ErrAlreadyRunning), and the reader
// cannot open while ingest runs.
func OpenLedgerReader(dbPath string) (*LedgerReader, error) {
	if !filepath.IsAbs(dbPath) {
		return nil, fmt.Errorf("ingest ledger %q: path must be absolute", dbPath)
	}
	info, err := os.Lstat(dbPath)
	if err != nil {
		return nil, fmt.Errorf("ingest ledger: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("ingest ledger %s: not a regular file", dbPath)
	}
	unlock, err := lockExisting(dbPath)
	if err != nil {
		return nil, err
	}
	r, err := openImmutable(dbPath)
	if err != nil {
		unlock()
		return nil, err
	}
	r.unlock = unlock
	return r, nil
}

// lockExisting takes the same flock as acquireLock on an ingest.lock that
// must already exist; it never creates the file and refuses a symlink.
func lockExisting(dbPath string) (func(), error) {
	lockPath := filepath.Join(filepath.Dir(dbPath), "ingest.lock")
	f, err := os.OpenFile(lockPath, os.O_RDWR|unix.O_NOFOLLOW, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("ingest ledger %s: no %s; run cogvault ingest once before reading the ledger", dbPath, lockPath)
	}
	if err != nil {
		return nil, fmt.Errorf("ingest ledger: open lock %s: %w", lockPath, err)
	}
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("ingest ledger: lock %s is not a regular file", lockPath)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("ingest ledger: flock %s: %w", lockPath, err)
	}
	return func() {
		unix.Flock(int(f.Fd()), unix.LOCK_UN)
		f.Close()
	}, nil
}

func openImmutable(dbPath string) (*LedgerReader, error) {
	if wal, err := os.Lstat(dbPath + "-wal"); err == nil && wal.Size() > 0 {
		return nil, fmt.Errorf("ingest ledger %s: has an un-checkpointed -wal file; run cogvault ingest once so the ledger is complete, then retry", dbPath)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("ingest ledger: %w", err)
	}
	dsn := (&url.URL{Scheme: "file", Path: dbPath, RawQuery: "mode=ro&immutable=1&_pragma=query_only(1)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("ingest ledger %s: %w", dbPath, err)
	}
	var name string
	err = db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'ingest_ledger'`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		db.Close()
		return nil, fmt.Errorf("ingest ledger %s: no ingest_ledger table", dbPath)
	}
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("ingest ledger %s: %w", dbPath, err)
	}
	return &LedgerReader{db: db}, nil
}

// Lookup returns the row for the ledger key, or found=false.
func (r *LedgerReader) Lookup(sourcePath, contentHash string) (LedgerEntry, bool, error) {
	var e LedgerEntry
	err := r.db.QueryRow(`SELECT COALESCE(status, ''), COALESCE(wiki_page, ''), COALESCE(digested_at, '') FROM ingest_ledger WHERE source_path = ? AND content_hash = ?`, sourcePath, contentHash).Scan(&e.Status, &e.WikiPage, &e.DigestedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return LedgerEntry{}, false, nil
	}
	if err != nil {
		return LedgerEntry{}, false, fmt.Errorf("ingest ledger lookup: %w", err)
	}
	return e, true, nil
}

func (r *LedgerReader) Close() error {
	err := r.db.Close()
	if r.unlock != nil {
		r.unlock()
	}
	return err
}
