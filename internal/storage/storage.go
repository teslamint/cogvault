package storage

type Storage interface {
	Read(path string) ([]byte, error)
	Write(path string, data []byte) error
	List(prefix string) ([]ListEntry, error)
	Exists(path string) (bool, error)
}

type ListEntry struct {
	Path  string
	Name  string
	IsDir bool
}
