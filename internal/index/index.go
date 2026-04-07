package index

import (
	"errors"

	"github.com/teslamint/cogvault/internal/adapter"
	"github.com/teslamint/cogvault/internal/storage"
)

var ErrConsistencySystemic = errors.New("systemic consistency error")

type Index interface {
	Add(path, content string, meta map[string]string) error
	Search(query string, limit int, scope string) ([]Result, error)
	Remove(path string) error
	Rebuild(store storage.Storage, adpt adapter.Adapter) error
	CheckConsistency(store storage.Storage, adpt adapter.Adapter, force bool) (added, removed, updated int, err error)
	GetMeta(path string) (*FileMeta, error)
	Close() error
}

type Result struct {
	Path    string  `json:"path"`
	Title   string  `json:"title"`
	Type    string  `json:"type"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"`
}

type FileMeta struct {
	Path        string `json:"path"`
	Title       string `json:"title"`
	Type        string `json:"type"`
	ContentHash string `json:"content_hash"`
	IndexedAt   string `json:"indexed_at"`
}
