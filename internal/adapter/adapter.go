package adapter

type Adapter interface {
	Name() string
	Scan(root string, exclude []string, fn func(path string) error) error
	Parse(root, relPath string, includeContent bool) (*Source, error)
}

type Source struct {
	Path           string            `json:"path"`
	Title          string            `json:"title"`
	Content        string            `json:"content,omitempty"`
	Frontmatter    map[string]any    `json:"frontmatter"`
	Links          []string          `json:"links"`
	Attachments    []string          `json:"attachments"`
	Tags           []string          `json:"tags"`
	DataviewFields map[string]string `json:"dataview_fields"`
	Aliases        []string          `json:"aliases"`
	SourceType     string            `json:"source_type"`
}
