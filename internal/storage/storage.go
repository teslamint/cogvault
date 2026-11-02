package storage

import "time"

type Storage interface {
	Read(path string) ([]byte, error)
	Write(path string, data []byte) error
	Delete(path string) error
	Move(src, dst string) error
	List(prefix string) ([]ListEntry, error)
	Exists(path string) (bool, error)
	IsFile(path string) (bool, error)
	Stat(path string) (size int64, mtime time.Time, err error)
	// SameFile reports whether a and b name the same file, such as two
	// Unicode-normalization forms of one name on APFS.
	SameFile(a, b string) (bool, error)
}

type ListEntry struct {
	Path  string
	Name  string
	IsDir bool
}
