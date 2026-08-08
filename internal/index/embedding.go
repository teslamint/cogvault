package index

import (
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

type EmbeddingRow struct {
	Path        string
	ContentHash string
	Model       string
	Vector      []float32
	Dims        int
	UpdatedAt   string
}

func (s *SQLiteIndex) InitEmbeddingsTable() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS embeddings (
		path TEXT PRIMARY KEY,
		content_hash TEXT NOT NULL,
		model TEXT NOT NULL,
		vector BLOB NOT NULL,
		dims INTEGER NOT NULL,
		updated_at TEXT NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("index: create embeddings table: %w", err)
	}
	return nil
}

func (s *SQLiteIndex) StoreEmbedding(path string, contentHash, model string, vec []float32) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	blob := vecToBlob(vec)
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO embeddings(path, content_hash, model, vector, dims, updated_at) VALUES (?, ?, ?, ?, ?, ?)`,
		normalizePath(path), contentHash, model, blob, len(vec), time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("index: store embedding %s: %w", path, err)
	}
	return nil
}

func (s *SQLiteIndex) GetEmbedding(path, model string) (*EmbeddingRow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var row EmbeddingRow
	var blob []byte
	err := s.db.QueryRow(
		`SELECT path, content_hash, model, vector, dims, updated_at FROM embeddings WHERE path = ? AND model = ?`,
		normalizePath(path), model,
	).Scan(&row.Path, &row.ContentHash, &row.Model, &blob, &row.Dims, &row.UpdatedAt)
	if err != nil {
		return nil, err
	}
	row.Vector = blobToVec(blob)
	return &row, nil
}

func (s *SQLiteIndex) StalePaths(model string) ([]StaleEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT f.path, f.content_hash
		FROM file_meta f
		LEFT JOIN embeddings e ON f.path = e.path AND e.model = ?
		WHERE e.path IS NULL OR e.content_hash != f.content_hash
	`, model)
	if err != nil {
		return nil, fmt.Errorf("index: stale paths: %w", err)
	}
	defer rows.Close()

	var result []StaleEntry
	for rows.Next() {
		var se StaleEntry
		if err := rows.Scan(&se.Path, &se.ContentHash); err != nil {
			return nil, fmt.Errorf("index: stale paths scan: %w", err)
		}
		result = append(result, se)
	}
	return result, rows.Err()
}

type StaleEntry struct {
	Path        string
	ContentHash string
}

func (s *SQLiteIndex) AllEmbeddings(model string) ([]EmbeddingRow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		`SELECT path, content_hash, model, vector, dims, updated_at FROM embeddings WHERE model = ?`, model,
	)
	if err != nil {
		return nil, fmt.Errorf("index: all embeddings: %w", err)
	}
	defer rows.Close()

	var result []EmbeddingRow
	for rows.Next() {
		var row EmbeddingRow
		var blob []byte
		if err := rows.Scan(&row.Path, &row.ContentHash, &row.Model, &blob, &row.Dims, &row.UpdatedAt); err != nil {
			return nil, fmt.Errorf("index: all embeddings scan: %w", err)
		}
		row.Vector = blobToVec(blob)
		result = append(result, row)
	}
	return result, rows.Err()
}

func (s *SQLiteIndex) EmbeddingCount(model string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var count int
	err := s.db.QueryRow(`SELECT count(*) FROM embeddings WHERE model = ?`, model).Scan(&count)
	return count, err
}

func vecToBlob(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

func blobToVec(b []byte) []float32 {
	n := len(b) / 4
	v := make([]float32, n)
	for i := 0; i < n; i++ {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}

func dotProduct(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var sum float64
	for i := range a {
		sum += float64(a[i]) * float64(b[i])
	}
	return sum
}
