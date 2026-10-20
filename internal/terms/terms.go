package terms

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/adrg/frontmatter"
	"gopkg.in/yaml.v3"
)

var yamlFrontmatter = frontmatter.NewFormat("---", "---", decodeMetadata)

func decodeMetadata(data []byte, value any) error {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return err
	}
	preserveTimestampText(&document)
	return document.Decode(value)
}

func preserveTimestampText(node *yaml.Node) {
	if node.Kind == yaml.ScalarNode && node.Tag == "!!timestamp" {
		// Metadata dates and evidence are text, not timezone-normalized instants.
		node.Tag = "!!str"
	}
	for _, child := range node.Content {
		preserveTimestampText(child)
	}
}

// Parse returns YAML frontmatter metadata and the unchanged Markdown body.
func Parse(content []byte) (map[string]any, []byte, error) {
	metadata := make(map[string]any)
	detect := bytes.TrimPrefix(content, []byte{0xef, 0xbb, 0xbf})
	body, err := frontmatter.Parse(bytes.NewReader(detect), &metadata, yamlFrontmatter)
	if errors.Is(err, frontmatter.ErrNotFound) {
		return metadata, content, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if metadata == nil {
		metadata = make(map[string]any)
	}
	return metadata, body, nil
}

// Encode renders metadata as YAML frontmatter and appends body without changes.
func Encode(metadata map[string]any, body []byte) ([]byte, error) {
	encoded, err := yaml.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("encode usage terms metadata: %w", err)
	}
	result := make([]byte, 0, len(encoded)+len(body)+8)
	result = append(result, "---\n"...)
	result = append(result, encoded...)
	result = append(result, "---\n"...)
	result = append(result, body...)
	return result, nil
}

// Automatic selects the first supplied usage_terms record and marks it unverified.
func Automatic(source, previous, generated map[string]any) map[string]any {
	result := make(map[string]any)
	for key, value := range generated {
		result[key] = value
	}
	var selected any
	for _, metadata := range []map[string]any{source, previous, generated} {
		if record, ok := metadata["usage_terms"]; ok && record != nil {
			selected = record
			break
		}
	}
	if selected == nil {
		result["usage_terms"] = map[string]any{"status": "unverified"}
		return result
	}
	record, ok := selected.(map[string]any)
	// Preserve malformed supplied records rather than substituting a generated
	// candidate that could hide or overwrite the caller's data.
	if !ok {
		result["usage_terms"] = selected
		return result
	}
	copyRecord := make(map[string]any, len(record)+1)
	for key, value := range record {
		if key != "reviewed_by" && key != "reviewed_at" && key != "status" {
			copyRecord[key] = value
		}
	}
	copyRecord["status"] = "unverified"
	result["usage_terms"] = copyRecord
	return result
}

var protectedFields = []string{"purpose", "conditions", "scope", "where", "level", "evidence"}

// CheckOverwrite rejects Markdown replacements that would erase existing terms evidence.
func CheckOverwrite(existing, incoming []byte) error {
	oldMetadata, _, err := Parse(existing)
	if err != nil {
		// A broken page must remain repairable; only parsed declarations can
		// be compared structurally. This does not make the old record verified.
		return nil
	}
	oldTerms, ok := oldMetadata["usage_terms"].(map[string]any)
	if !ok {
		return nil
	}
	newMetadata, _, err := Parse(incoming)
	if err != nil {
		return fmt.Errorf("parse incoming Markdown frontmatter: %w", err)
	}
	newTerms, _ := newMetadata["usage_terms"].(map[string]any)
	for _, field := range protectedFields {
		if err := preserveValue(oldTerms[field], newTerms[field], "usage_terms."+field); err != nil {
			return err
		}
	}
	return nil
}

func preserveValue(existing, incoming any, path string) error {
	if !nonempty(existing) {
		return nil
	}
	switch old := existing.(type) {
	case string:
		text, ok := incoming.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return fmt.Errorf("overwrite would drop text in %s", path)
		}
	case []any:
		list, ok := incoming.([]any)
		if !ok || !nonempty(list) {
			return fmt.Errorf("overwrite would drop list in %s", path)
		}
	case map[string]any:
		current, ok := incoming.(map[string]any)
		if !ok {
			return fmt.Errorf("overwrite would drop mapping in %s", path)
		}
		for key, value := range old {
			if err := preserveValue(value, current[key], path+"."+key); err != nil {
				return err
			}
		}
	}
	return nil
}

func nonempty(value any) bool {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v) != ""
	case []any:
		for _, item := range v {
			if nonempty(item) {
				return true
			}
		}
	case map[string]any:
		for _, item := range v {
			if nonempty(item) {
				return true
			}
		}
	}
	return false
}
