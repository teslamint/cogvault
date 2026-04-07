package index

import (
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"

	"github.com/teslamint/cogvault/internal/adapter"
	"github.com/teslamint/cogvault/internal/config"
	cverr "github.com/teslamint/cogvault/internal/errors"
	"github.com/teslamint/cogvault/internal/storage"
)

type SQLiteIndex struct {
	db              *sql.DB
	cfg             *config.Config
	root            string
	lastConsistency atomic.Int64
	mu              sync.RWMutex
	ccMu            sync.Mutex
	useTrigram      bool
}

func NewSQLiteIndex(root, dbPath string, cfg *config.Config) (*SQLiteIndex, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("index: open db: %w", err)
	}

	s := &SQLiteIndex{
		db:   db,
		cfg:  cfg,
		root: root,
	}

	if err := s.initSchema(); err != nil {
		db.Close()
		return nil, err
	}

	return s, nil
}

func (s *SQLiteIndex) initSchema() error {
	if _, err := s.db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		return fmt.Errorf("index: set WAL: %w", err)
	}

	// Detect existing tokenizer or create new FTS table
	var existingSQL sql.NullString
	s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='wiki_fts'`).Scan(&existingSQL)

	if existingSQL.Valid {
		s.useTrigram = strings.Contains(existingSQL.String, "trigram")
	} else {
		_, err := s.db.Exec(`CREATE VIRTUAL TABLE wiki_fts USING fts5(path, title, content, tags, tokenize='trigram')`)
		if err != nil {
			slog.Warn("FTS5 trigram not supported, falling back to unicode61", "error", err)
			_, err = s.db.Exec(`CREATE VIRTUAL TABLE wiki_fts USING fts5(path, title, content, tags, tokenize='unicode61')`)
			if err != nil {
				return fmt.Errorf("index: create FTS table: %w", err)
			}
			s.useTrigram = false
		} else {
			s.useTrigram = true
		}
	}

	// Check file_meta schema and migrate if needed
	needsRecreate := false
	rows, err := s.db.Query(`PRAGMA table_info(file_meta)`)
	if err == nil {
		defer rows.Close()
		var hasModTime bool
		var colCount int
		for rows.Next() {
			var cid int
			var name, typ string
			var notNull int
			var dfltValue sql.NullString
			var pk int
			if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
				break
			}
			colCount++
			if name == "mod_time" {
				hasModTime = true
			}
		}
		if err := rows.Err(); err != nil {
			slog.Warn("index: error reading table_info", "error", err)
		}
		if colCount > 0 && hasModTime {
			needsRecreate = true
		}
	}

	if needsRecreate {
		slog.Info("index: migrating old schema (dropping wiki_fts + file_meta)")
		s.db.Exec(`DROP TABLE IF EXISTS wiki_fts`)
		s.db.Exec(`DROP TABLE IF EXISTS file_meta`)

		_, err := s.db.Exec(`CREATE VIRTUAL TABLE wiki_fts USING fts5(path, title, content, tags, tokenize='trigram')`)
		if err != nil {
			slog.Warn("FTS5 trigram not supported after migration, falling back to unicode61", "error", err)
			_, err = s.db.Exec(`CREATE VIRTUAL TABLE wiki_fts USING fts5(path, title, content, tags, tokenize='unicode61')`)
			if err != nil {
				return fmt.Errorf("index: create FTS table after migration: %w", err)
			}
			s.useTrigram = false
		} else {
			s.useTrigram = true
		}
	}

	_, err = s.db.Exec(`CREATE TABLE IF NOT EXISTS file_meta (
		path TEXT PRIMARY KEY,
		title TEXT DEFAULT '',
		type TEXT DEFAULT '',
		content_hash TEXT NOT NULL,
		indexed_at TEXT NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("index: create file_meta table: %w", err)
	}

	return nil
}

func (s *SQLiteIndex) Close() error {
	return s.db.Close()
}

func (s *SQLiteIndex) Add(path, content string, meta map[string]string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("index.Add %s: %w", path, err)
	}
	defer tx.Rollback()

	if err := s.addTx(tx, normalizePath(path), []byte(content), meta); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteIndex) addTx(tx *sql.Tx, path string, content []byte, meta map[string]string) error {
	hash := contentHash(content)
	title := meta["title"]
	typ := meta["type"]
	tags := meta["tags"]
	indexedAt := time.Now().UTC().Format(time.RFC3339)

	if _, err := tx.Exec(`DELETE FROM wiki_fts WHERE path = ?`, path); err != nil {
		return fmt.Errorf("index.Add %s: %w", path, err)
	}
	if _, err := tx.Exec(`INSERT INTO wiki_fts(path, title, content, tags) VALUES (?, ?, ?, ?)`,
		path, title, string(content), tags); err != nil {
		return fmt.Errorf("index.Add %s: %w", path, err)
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO file_meta(path, title, type, content_hash, indexed_at) VALUES (?, ?, ?, ?, ?)`,
		path, title, typ, hash, indexedAt); err != nil {
		return fmt.Errorf("index.Add %s: %w", path, err)
	}
	return nil
}

func (s *SQLiteIndex) Remove(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("index.Remove %s: %w", path, err)
	}
	defer tx.Rollback()

	if err := s.removeTx(tx, normalizePath(path)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteIndex) removeTx(tx *sql.Tx, path string) error {
	if _, err := tx.Exec(`DELETE FROM wiki_fts WHERE path = ?`, path); err != nil {
		return fmt.Errorf("index.Remove %s: %w", path, err)
	}
	if _, err := tx.Exec(`DELETE FROM file_meta WHERE path = ?`, path); err != nil {
		return fmt.Errorf("index.Remove %s: %w", path, err)
	}
	return nil
}

func (s *SQLiteIndex) GetMeta(path string) (*FileMeta, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	p := normalizePath(path)
	var fm FileMeta
	err := s.db.QueryRow(
		`SELECT path, title, type, content_hash, indexed_at FROM file_meta WHERE path = ?`, p,
	).Scan(&fm.Path, &fm.Title, &fm.Type, &fm.ContentHash, &fm.IndexedAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("index.GetMeta %s: %w", p, cverr.ErrNotFound)
		}
		return nil, fmt.Errorf("index.GetMeta %s: %w", p, err)
	}
	return &fm, nil
}

func (s *SQLiteIndex) Rebuild(store storage.Storage, adpt adapter.Adapter) error {
	s.mu.Lock()
	if _, err := s.db.Exec(`DELETE FROM wiki_fts`); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("index.Rebuild: %w", err)
	}
	if _, err := s.db.Exec(`DELETE FROM file_meta`); err != nil {
		s.mu.Unlock()
		return fmt.Errorf("index.Rebuild: %w", err)
	}
	s.mu.Unlock()

	_, _, _, err := s.CheckConsistency(store, adpt, true)
	return err
}

func (s *SQLiteIndex) Search(query string, limit int, scope string) ([]Result, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if strings.TrimSpace(query) == "" {
		return []Result{}, nil
	}

	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	if scope == "" {
		scope = "all"
	}

	runeCount := utf8.RuneCountInString(query)

	if runeCount >= 3 && s.useTrigram {
		return s.searchFTS(query, limit, scope)
	}
	return s.searchLIKE(query, limit, scope)
}

func (s *SQLiteIndex) searchFTS(query string, limit int, scope string) ([]Result, error) {
	escaped := escapeMatch(query)

	q := `SELECT f.path, f.title, f.type, snippet(wiki_fts, 2, '', '', '...', 32), rank
		FROM wiki_fts JOIN file_meta f ON wiki_fts.path = f.path
		WHERE wiki_fts MATCH ?`
	args := []any{escaped}

	q, args = appendScopeFilter(q, args, scope, s.cfg.WikiDir)
	q += ` ORDER BY rank LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("index.Search: %w", err)
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var r Result
		var rank float64
		if err := rows.Scan(&r.Path, &r.Title, &r.Type, &r.Snippet, &rank); err != nil {
			return nil, fmt.Errorf("index.Search: %w", err)
		}
		r.Score = -rank
		results = append(results, r)
	}
	if results == nil {
		results = []Result{}
	}
	return results, rows.Err()
}

func (s *SQLiteIndex) searchLIKE(query string, limit int, scope string) ([]Result, error) {
	pattern := "%" + escapeLike(query) + "%"

	q := `SELECT f.path, f.title, f.type, wiki_fts.content
		FROM wiki_fts JOIN file_meta f ON wiki_fts.path = f.path
		WHERE wiki_fts.content LIKE ? ESCAPE '\'`
	args := []any{pattern}

	q, args = appendScopeFilter(q, args, scope, s.cfg.WikiDir)
	q += ` LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("index.Search: %w", err)
	}
	defer rows.Close()

	var results []Result
	for rows.Next() {
		var r Result
		var content string
		if err := rows.Scan(&r.Path, &r.Title, &r.Type, &content); err != nil {
			return nil, fmt.Errorf("index.Search: %w", err)
		}
		r.Snippet = likeSnippet(content, query)
		results = append(results, r)
	}
	if results == nil {
		results = []Result{}
	}
	return results, rows.Err()
}

func (s *SQLiteIndex) CheckConsistency(store storage.Storage, adpt adapter.Adapter, force bool) (int, int, int, error) {
	interval := time.Duration(s.cfg.ConsistencyInterval) * time.Second

	// Lock-free interval check
	now := time.Now().UnixNano()
	last := s.lastConsistency.Load()
	if !force && now-last < int64(interval) {
		return 0, 0, 0, nil
	}

	s.ccMu.Lock()
	defer s.ccMu.Unlock()

	// Double-check under lock
	if !force && time.Now().UnixNano()-s.lastConsistency.Load() < int64(interval) {
		return 0, 0, 0, nil
	}

	// Read current index state
	s.mu.RLock()
	indexed, err := s.loadIndexedHashes()
	s.mu.RUnlock()
	if err != nil {
		return 0, 0, 0, fmt.Errorf("index.CheckConsistency: %w: %w", ErrConsistencySystemic, err)
	}

	var (
		toAdd    []changeEntry
		toUpdate []changeEntry
		errs     []error
	)

	// Scan + classify
	scanErr := adpt.Scan(s.root, s.cfg.AllExcluded(), func(path string) error {
		p := normalizePath(path)

		data, err := store.Read(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("read %s: %w", p, err))
			delete(indexed, p)
			return nil
		}

		hash := contentHash(data)

		if storedHash, ok := indexed[p]; ok {
			delete(indexed, p)
			if hash != storedHash {
				src, err := adpt.Parse(s.root, path, false)
				if err != nil {
					errs = append(errs, fmt.Errorf("parse %s: %w", p, err))
					return nil
				}
				toUpdate = append(toUpdate, changeEntry{path: p, content: data, meta: BuildMeta(src)})
			}
		} else {
			src, err := adpt.Parse(s.root, path, false)
			if err != nil {
				errs = append(errs, fmt.Errorf("parse %s: %w", p, err))
				return nil
			}
			toAdd = append(toAdd, changeEntry{path: p, content: data, meta: BuildMeta(src)})
		}
		return nil
	})

	if scanErr != nil {
		return 0, 0, 0, fmt.Errorf("index.CheckConsistency: scan: %w: %w", ErrConsistencySystemic, scanErr)
	}

	// Remaining in indexed = deleted/excluded
	toRemove := make([]string, 0, len(indexed))
	for p := range indexed {
		toRemove = append(toRemove, p)
	}

	// Apply all changes in a single transaction (all-or-nothing)
	var added, removed, updated int
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, txErr := s.db.Begin()
	if txErr != nil {
		return 0, 0, 0, fmt.Errorf("index.CheckConsistency: begin tx: %w: %w", ErrConsistencySystemic, txErr)
	}
	defer tx.Rollback()

	for _, p := range toRemove {
		if err := s.removeTx(tx, p); err != nil {
			return 0, 0, 0, fmt.Errorf("index.CheckConsistency: apply: %w: %w", ErrConsistencySystemic, err)
		}
		removed++
	}
	for _, e := range toUpdate {
		if err := s.addTx(tx, e.path, e.content, e.meta); err != nil {
			return 0, 0, 0, fmt.Errorf("index.CheckConsistency: apply: %w: %w", ErrConsistencySystemic, err)
		}
		updated++
	}
	for _, e := range toAdd {
		if err := s.addTx(tx, e.path, e.content, e.meta); err != nil {
			return 0, 0, 0, fmt.Errorf("index.CheckConsistency: apply: %w: %w", ErrConsistencySystemic, err)
		}
		added++
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, 0, fmt.Errorf("index.CheckConsistency: commit: %w: %w", ErrConsistencySystemic, err)
	}

	s.lastConsistency.Store(time.Now().UnixNano())

	return added, removed, updated, errors.Join(errs...)
}

func (s *SQLiteIndex) loadIndexedHashes() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT path, content_hash FROM file_meta`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	indexed := make(map[string]string)
	for rows.Next() {
		var path, hash string
		if err := rows.Scan(&path, &hash); err != nil {
			return nil, err
		}
		indexed[path] = hash
	}
	return indexed, rows.Err()
}

// BuildMeta extracts metadata from a parsed Source into the map format expected by Add.
func BuildMeta(src *adapter.Source) map[string]string {
	pageType, _ := src.Frontmatter["type"].(string)
	return map[string]string{
		"title": src.Title,
		"type":  pageType,
		"tags":  strings.Join(src.Tags, ","),
	}
}

type changeEntry struct {
	path    string
	content []byte
	meta    map[string]string
}

func appendScopeFilter(q string, args []any, scope, wikiDir string) (string, []any) {
	switch scope {
	case "wiki":
		q += ` AND f.path LIKE ? ESCAPE '\'`
		args = append(args, escapeLike(normalizePath(wikiDir))+"/%")
	case "vault":
		q += ` AND f.path NOT LIKE ? ESCAPE '\'`
		args = append(args, escapeLike(normalizePath(wikiDir))+"/%")
	}
	return q, args
}

func normalizePath(p string) string {
	cleaned := filepath.Clean(p)
	return strings.ReplaceAll(cleaned, `\`, "/")
}

func contentHash(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}

func escapeMatch(query string) string {
	return `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func likeSnippet(content, query string) string {
	runes := []rune(strings.ToLower(content))
	qRunes := []rune(strings.ToLower(query))
	origRunes := []rune(content)
	idx := runeIndex(runes, qRunes)
	if idx < 0 {
		return ""
	}

	start := idx - 32
	if start < 0 {
		start = 0
	}
	end := idx + len(qRunes) + 32
	if end > len(origRunes) {
		end = len(origRunes)
	}

	snippet := string(origRunes[start:end])
	if start > 0 {
		snippet = "..." + snippet
	}
	if end < len(origRunes) {
		snippet = snippet + "..."
	}
	return snippet
}

func runeIndex(text, pattern []rune) int {
	if len(pattern) == 0 {
		return 0
	}
	if len(pattern) > len(text) {
		return -1
	}
	for i := 0; i <= len(text)-len(pattern); i++ {
		match := true
		for j := range pattern {
			if text[i+j] != pattern[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
