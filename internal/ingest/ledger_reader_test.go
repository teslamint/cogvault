package ingest

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// ledgerFixture writes one ledger row with the same openLedger ingest uses
// and closes it. It never creates ingest.lock.
func ledgerFixture(t *testing.T) (dir, dbPath string) {
	t.Helper()
	dir = t.TempDir()
	dbPath = filepath.Join(dir, "cogvault.db")
	l, err := openLedger(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.upsert(ledgerRow{sourcePath: "/src/a.md", contentHash: "abc", status: "success", wikiPage: "sources/a.md", digestedAt: "2026-01-01T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
	if err := l.close(); err != nil {
		t.Fatal(err)
	}
	return dir, dbPath
}

func assertUnchanged(t *testing.T, dir, dbPath string, before []string, info os.FileInfo) {
	t.Helper()
	if got := dirNames(t, dir); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Fatalf("directory changed: before %v, after %v", before, got)
	}
	after, err := os.Stat(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(info.ModTime()) || after.Size() != info.Size() {
		t.Fatal("ledger file changed")
	}
}

func TestLedgerReaderWithoutLockFileRefusesAndCreatesNothing(t *testing.T) {
	dir, dbPath := ledgerFixture(t)
	before := dirNames(t, dir)
	if strings.Contains(strings.Join(before, ","), "ingest.lock") {
		t.Fatalf("fixture already has a lock: %v", before)
	}
	info, _ := os.Stat(dbPath)
	if _, err := OpenLedgerReader(dbPath); err == nil || !strings.Contains(err.Error(), "ingest.lock") {
		t.Fatalf("err = %v, want refusal naming ingest.lock", err)
	}
	assertUnchanged(t, dir, dbPath, before, info)
}

func TestLedgerReaderReadsWithoutWriting(t *testing.T) {
	dir, dbPath := ledgerFixture(t)
	unlock, err := acquireLock(dbPath) // a completed ingest run leaves this file
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	before := dirNames(t, dir)
	info, _ := os.Stat(dbPath)

	r, err := OpenLedgerReader(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	e, found, err := r.Lookup("/src/a.md", "abc")
	if err != nil || !found || e.Status != "success" || e.WikiPage != "sources/a.md" {
		t.Fatalf("Lookup = %+v %v %v", e, found, err)
	}
	if _, found, err := r.Lookup("/src/a.md", "other"); err != nil || found {
		t.Fatalf("missing key: found=%v err=%v", found, err)
	}
	if _, err := r.db.Exec(`DELETE FROM ingest_ledger`); err == nil {
		t.Fatal("a write through the reader connection succeeded; it must be read-only")
	}
	if _, err := acquireLock(dbPath); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("ingest lock while the reader is open: err = %v, want ErrAlreadyRunning", err)
	}
	r.Close()
	assertUnchanged(t, dir, dbPath, before, info)

	held, err := acquireLock(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer held()
	if _, err := OpenLedgerReader(dbPath); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("reader while ingest holds the lock: err = %v, want ErrAlreadyRunning", err)
	}
}

func TestLedgerReaderRefusesMissingWrongOrUncheckpointedDatabase(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.db")
	if _, err := OpenLedgerReader(missing); err == nil {
		t.Fatal("missing ledger opened")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("opening a missing ledger created it: %v", err)
	}
	if _, err := OpenLedgerReader("relative.db"); err == nil {
		t.Fatal("relative ledger path accepted")
	}

	empty := filepath.Join(dir, "empty.db")
	db, err := sql.Open("sqlite", empty)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE other (x)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	unlock, err := acquireLock(empty)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if _, err := OpenLedgerReader(empty); err == nil || !strings.Contains(err.Error(), "no ingest_ledger table") {
		t.Fatalf("database without the ledger table: err = %v", err)
	}

	link := filepath.Join(dir, "link.db")
	if err := os.Symlink(empty, link); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenLedgerReader(link); err == nil {
		t.Fatal("symlinked ledger accepted")
	}

	_, ledger := ledgerFixture(t)
	unlock, err = acquireLock(ledger)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	if err := os.WriteFile(ledger+"-wal", []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenLedgerReader(ledger); err == nil || !strings.Contains(err.Error(), "-wal") {
		t.Fatalf("leftover -wal: err = %v", err)
	}
}
