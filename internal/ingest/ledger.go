package ingest

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"
)

type ledgerRow struct {
	sourcePath  string
	contentHash string
	sourceDir   string
	digestedAt  string
	wikiPage    string
	status      string
	attempts    int
	lastError   string
	runOrigin   string
}

type ledger struct {
	db *sql.DB
}

const ledgerDDL = `CREATE TABLE IF NOT EXISTS ingest_ledger (
	source_path TEXT,
	content_hash TEXT,
	source_dir TEXT,
	digested_at TEXT,
	wiki_page TEXT,
	status TEXT,
	attempts INTEGER,
	last_error TEXT,
	run_origin TEXT,
	PRIMARY KEY (source_path, content_hash)
)`

func dsnWithPragmas(dbPath string) string {
	sep := "?"
	if strings.Contains(dbPath, "?") {
		sep = "&"
	}
	return dbPath + sep + "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
}

func openLedger(dbPath string) (*ledger, error) {
	db, err := sql.Open("sqlite", dsnWithPragmas(dbPath))
	if err != nil {
		return nil, fmt.Errorf("ingest.openLedger %s: %w", dbPath, err)
	}
	if _, err := db.Exec(ledgerDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("ingest.openLedger %s: %w", dbPath, err)
	}
	return &ledger{db: db}, nil
}

func (l *ledger) close() error {
	if l.db == nil {
		return nil
	}
	return l.db.Close()
}

func (l *ledger) lookup(sourcePath, contentHash string) (*ledgerRow, bool, error) {
	row := &ledgerRow{sourcePath: sourcePath, contentHash: contentHash}
	err := l.db.QueryRow(
		`SELECT source_dir, digested_at, wiki_page, status, attempts, last_error, run_origin
		 FROM ingest_ledger WHERE source_path = ? AND content_hash = ?`,
		sourcePath, contentHash,
	).Scan(&row.sourceDir, &row.digestedAt, &row.wikiPage, &row.status, &row.attempts, &row.lastError, &row.runOrigin)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("ingest.ledger.lookup %s: %w", sourcePath, err)
	}
	return row, true, nil
}

func (l *ledger) wikiPageTakenByOther(wikiPage, sourcePath string) (bool, error) {
	var n int
	err := l.db.QueryRow(
		`SELECT COUNT(1) FROM ingest_ledger WHERE wiki_page = ? AND source_path <> ?`,
		wikiPage, sourcePath,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("ingest.ledger.wikiPageTakenByOther %s: %w", wikiPage, err)
	}
	return n > 0, nil
}

func (l *ledger) supersedePrevSuccess(sourcePath string) error {
	_, err := l.db.Exec(
		`UPDATE ingest_ledger SET status = 'superseded' WHERE source_path = ? AND status = 'success'`,
		sourcePath,
	)
	if err != nil {
		return fmt.Errorf("ingest.ledger.supersedePrevSuccess %s: %w", sourcePath, err)
	}
	return nil
}

func (l *ledger) upsert(row ledgerRow) error {
	_, err := l.db.Exec(
		`INSERT OR REPLACE INTO ingest_ledger
		 (source_path, content_hash, source_dir, digested_at, wiki_page, status, attempts, last_error, run_origin)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.sourcePath, row.contentHash, row.sourceDir, row.digestedAt,
		row.wikiPage, row.status, row.attempts, row.lastError, row.runOrigin,
	)
	if err != nil {
		return fmt.Errorf("ingest.ledger.upsert %s: %w", row.sourcePath, err)
	}
	return nil
}
