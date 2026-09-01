package ingest

import (
	"database/sql"
	"fmt"
	_ "modernc.org/sqlite"
	"strings"
)

type ledgerRow struct {
	sourcePath, contentHash, sourceDir, digestedAt, wikiPage, status string
	attempts                                                         int
	lastError, runOrigin, llmModel, digestProfile                    string
}
type ledger struct{ db *sql.DB }

const ledgerDDL = `CREATE TABLE IF NOT EXISTS ingest_ledger (
 source_path TEXT, content_hash TEXT, source_dir TEXT, digested_at TEXT, wiki_page TEXT,
 status TEXT, attempts INTEGER, last_error TEXT, run_origin TEXT,
 llm_model TEXT NOT NULL DEFAULT '', digest_profile TEXT NOT NULL DEFAULT '',
 PRIMARY KEY (source_path, content_hash))`

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
	if err := migrateDigestProfile(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("ingest.openLedger %s: %w", dbPath, err)
	}
	return &ledger{db: db}, nil
}
func columnExists(db *sql.DB, want string) (bool, error) {
	rows, err := db.Query(`PRAGMA table_info(ingest_ledger)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, nn, pk int
		var n, typ string
		var d sql.NullString
		if err := rows.Scan(&cid, &n, &typ, &nn, &d, &pk); err != nil {
			return false, err
		}
		if n == want {
			return true, nil
		}
	}
	return false, rows.Err()
}
func migrateLLMModel(db *sql.DB) error {
	has, err := columnExists(db, "llm_model")
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	if _, err = db.Exec(`ALTER TABLE ingest_ledger ADD COLUMN llm_model TEXT NOT NULL DEFAULT ''`); err != nil {
		has2, e := columnExists(db, "llm_model")
		if e != nil {
			return e
		}
		if !has2 {
			return err
		}
	}
	return nil
}
func migrateDigestProfile(db *sql.DB) error {
	has, err := columnExists(db, "digest_profile")
	if err != nil {
		return err
	}
	if has {
		return nil
	}
	if _, err = db.Exec(`ALTER TABLE ingest_ledger ADD COLUMN digest_profile TEXT NOT NULL DEFAULT ''`); err != nil {
		has2, e := columnExists(db, "digest_profile")
		if e != nil {
			return e
		}
		if !has2 {
			return err
		}
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
	r := &ledgerRow{sourcePath: sourcePath, contentHash: contentHash}
	err := l.db.QueryRow(`SELECT source_dir,digested_at,wiki_page,status,attempts,last_error,run_origin,llm_model,digest_profile FROM ingest_ledger WHERE source_path=? AND content_hash=?`, sourcePath, contentHash).Scan(&r.sourceDir, &r.digestedAt, &r.wikiPage, &r.status, &r.attempts, &r.lastError, &r.runOrigin, &r.llmModel, &r.digestProfile)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("ingest.ledger.lookup %s: %w", sourcePath, err)
	}
	return r, true, nil
}
func (l *ledger) wikiPageTakenByOther(wikiPage, sourcePath string) (bool, error) {
	var n int
	err := l.db.QueryRow(`SELECT COUNT(1) FROM ingest_ledger WHERE wiki_page=? AND source_path<>?`, wikiPage, sourcePath).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("ingest.ledger.wikiPageTakenByOther %s: %w", sourcePath, err)
	}
	return n > 0, nil
}
func (l *ledger) supersedePrevSuccess(sourcePath string) error {
	_, err := l.db.Exec(`UPDATE ingest_ledger SET status='superseded' WHERE source_path=? AND status='success'`, sourcePath)
	if err != nil {
		return fmt.Errorf("ingest.ledger.supersedePrevSuccess %s: %w", sourcePath, err)
	}
	return nil
}
func (l *ledger) successRows() ([]ledgerRow, error) {
	rows, err := l.db.Query(`SELECT source_path,content_hash,source_dir,digested_at,wiki_page,status,attempts,last_error,run_origin,llm_model,digest_profile FROM ingest_ledger WHERE status='success'`)
	if err != nil {
		return nil, fmt.Errorf("ingest.ledger.successRows: %w", err)
	}
	defer rows.Close()
	var out []ledgerRow
	for rows.Next() {
		var r ledgerRow
		if err := rows.Scan(&r.sourcePath, &r.contentHash, &r.sourceDir, &r.digestedAt, &r.wikiPage, &r.status, &r.attempts, &r.lastError, &r.runOrigin, &r.llmModel, &r.digestProfile); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (l *ledger) attentionRows(model string, profiles ...string) ([]ledgerRow, error) {
	profile := model
	if len(profiles) > 0 {
		profile = profiles[0]
	}
	rows, err := l.db.Query(`WITH latest AS (SELECT source_path,MAX(rowid) max_id FROM ingest_ledger GROUP BY source_path) SELECT l.source_path,l.content_hash,l.digested_at,l.last_error,l.attempts,l.llm_model,l.status,l.digest_profile FROM ingest_ledger l JOIN latest x ON l.rowid=x.max_id WHERE l.llm_model=? AND (l.digest_profile=? OR l.digest_profile='') AND ((l.status='failed' AND l.attempts>=?) OR l.status='refused')`, model, profile, maxAttempts)
	if err != nil {
		return nil, fmt.Errorf("ingest.ledger.attentionRows: %w", err)
	}
	defer rows.Close()
	var out []ledgerRow
	for rows.Next() {
		var r ledgerRow
		if err := rows.Scan(&r.sourcePath, &r.contentHash, &r.digestedAt, &r.lastError, &r.attempts, &r.llmModel, &r.status, &r.digestProfile); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (l *ledger) upsert(r ledgerRow) error {
	_, err := l.db.Exec(`INSERT OR REPLACE INTO ingest_ledger (source_path,content_hash,source_dir,digested_at,wiki_page,status,attempts,last_error,run_origin,llm_model,digest_profile) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, r.sourcePath, r.contentHash, r.sourceDir, r.digestedAt, r.wikiPage, r.status, r.attempts, r.lastError, r.runOrigin, r.llmModel, r.digestProfile)
	if err != nil {
		return fmt.Errorf("ingest.ledger.upsert %s: %w", r.sourcePath, err)
	}
	return nil
}
