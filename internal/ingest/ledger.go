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
	llmModel    string
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
	llm_model TEXT NOT NULL DEFAULT '',
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
	if err := migrateLLMModel(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("ingest.openLedger %s: %w", dbPath, err)
	}
	return &ledger{db: db}, nil
}

func migrateLLMModel(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(ingest_ledger)`)
	if err != nil {
		return err
	}
	has := false
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == "llm_model" {
			has = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if has {
		return nil
	}
	if _, err := db.Exec(`ALTER TABLE ingest_ledger ADD COLUMN llm_model TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	return nil
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
		`SELECT source_dir, digested_at, wiki_page, status, attempts, last_error, run_origin, llm_model
		 FROM ingest_ledger WHERE source_path = ? AND content_hash = ?`,
		sourcePath, contentHash,
	).Scan(&row.sourceDir, &row.digestedAt, &row.wikiPage, &row.status, &row.attempts, &row.lastError, &row.runOrigin, &row.llmModel)
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

func (l *ledger) successRows() ([]ledgerRow, error) {
	rows, err := l.db.Query(
		`SELECT source_path, content_hash, source_dir, digested_at, wiki_page, status, attempts, last_error, run_origin, llm_model
		 FROM ingest_ledger WHERE status = 'success'`)
	if err != nil {
		return nil, fmt.Errorf("ingest.ledger.successRows: %w", err)
	}
	defer rows.Close()

	var result []ledgerRow
	for rows.Next() {
		var r ledgerRow
		if err := rows.Scan(&r.sourcePath, &r.contentHash, &r.sourceDir, &r.digestedAt, &r.wikiPage, &r.status, &r.attempts, &r.lastError, &r.runOrigin, &r.llmModel); err != nil {
			return nil, fmt.Errorf("ingest.ledger.successRows scan: %w", err)
		}
		result = append(result, r)
	}
	return result, rows.Err()
}

func (l *ledger) attentionRows(model string) ([]ledgerRow, error) {
	rows, err := l.db.Query(
		`WITH latest AS (
			SELECT source_path, MAX(rowid) AS max_id
			FROM ingest_ledger
			WHERE (source_path, digested_at) IN (
				SELECT source_path, MAX(digested_at)
				FROM ingest_ledger
				GROUP BY source_path
			)
			GROUP BY source_path
		)
		SELECT l.source_path, l.content_hash, l.digested_at, l.last_error,
		       l.attempts, l.llm_model, l.status
		FROM ingest_ledger l
		JOIN latest lt ON l.rowid = lt.max_id
		WHERE l.llm_model = ?
		  AND ((l.status = 'failed' AND l.attempts >= 3)
		       OR l.status = 'refused')`,
		model,
	)
	if err != nil {
		return nil, fmt.Errorf("ingest.ledger.attentionRows: %w", err)
	}
	defer rows.Close()

	var result []ledgerRow
	for rows.Next() {
		var row ledgerRow
		if err := rows.Scan(
			&row.sourcePath,
			&row.contentHash,
			&row.digestedAt,
			&row.lastError,
			&row.attempts,
			&row.llmModel,
			&row.status,
		); err != nil {
			return nil, fmt.Errorf("ingest.ledger.attentionRows scan: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ingest.ledger.attentionRows rows: %w", err)
	}
	return result, nil
}

func (l *ledger) upsert(row ledgerRow) error {
	_, err := l.db.Exec(
		`INSERT OR REPLACE INTO ingest_ledger
		 (source_path, content_hash, source_dir, digested_at, wiki_page, status, attempts, last_error, run_origin, llm_model)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.sourcePath, row.contentHash, row.sourceDir, row.digestedAt,
		row.wikiPage, row.status, row.attempts, row.lastError, row.runOrigin, row.llmModel,
	)
	if err != nil {
		return fmt.Errorf("ingest.ledger.upsert %s: %w", row.sourcePath, err)
	}
	return nil
}
