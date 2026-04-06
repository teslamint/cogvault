package errors

import "errors"

var (
	ErrNotFound    = errors.New("not found")
	ErrPermission  = errors.New("permission denied")
	ErrTraversal   = errors.New("path traversal")
	ErrSymlink     = errors.New("symlink not allowed")
	ErrNotMarkdown = errors.New("not a markdown file")
)
