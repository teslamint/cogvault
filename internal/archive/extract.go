package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// HTMLTextExtractor names and versions the HTML text extractor. A change in
// its output must change this string, so an extraction is always tied to the
// code that made it.
const HTMLTextExtractor = "html-text/1"

// htmlTextLimits is recorded with every extraction so a reader of the text
// knows what it is not.
const htmlTextLimits = "parsed with Go encoding/xml in non-strict HTML mode; " +
	"script, style, noscript, template, iframe, object, embed, svg, math, canvas, and head content removed (title kept); " +
	"no scripts run, no CSS applied, no links, images, or other resources loaded; " +
	"block elements and table rows become line breaks, list items get '- ', headings get '#'; " +
	"input must be UTF-8; tables, forms, and layout are not reproduced"

// Extraction is one text extraction of an object.
type Extraction struct {
	Extractor      string `json:"extractor"`
	Status         string `json:"status"`
	OutputObjectID string `json:"output_object_id,omitempty"`
	Size           int64  `json:"size,omitempty"`
	Detail         string `json:"detail"`
	RecordedAt     string `json:"recorded_at"`
}

// rawTextElements hold text the HTML tokenizer must not read as markup.
var rawTextElements = []string{"script", "style"}

// removedElements are dropped with all their content.
var removedElements = map[string]bool{
	"script": true, "style": true, "noscript": true, "template": true, "iframe": true, "object": true,
	"embed": true, "svg": true, "math": true, "canvas": true, "head": true,
}

var blockElements = map[string]bool{
	"p": true, "div": true, "br": true, "section": true, "article": true, "header": true, "footer": true,
	"main": true, "nav": true, "aside": true, "blockquote": true, "pre": true, "table": true, "tr": true,
	"ul": true, "ol": true, "dl": true, "dt": true, "dd": true, "figure": true, "figcaption": true, "hr": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true, "li": true, "address": true,
}

// stripRawText removes the content of raw text elements, which HTML ends
// only at the matching close tag, so a "<" inside a script never reaches
// the XML tokenizer.
func stripRawText(src []byte) []byte {
	lower := bytes.ToLower(src)
	var out bytes.Buffer
	i := 0
	for i < len(src) {
		start, name := -1, ""
		for _, n := range rawTextElements {
			if j := bytes.Index(lower[i:], []byte("<"+n)); j >= 0 && (start < 0 || i+j < start) {
				k := i + j + 1 + len(n)
				if k >= len(lower) || lower[k] == '>' || lower[k] == '/' || unicode.IsSpace(rune(lower[k])) {
					start, name = i+j, n
				}
			}
		}
		if start < 0 {
			out.Write(src[i:])
			break
		}
		out.Write(src[i:start])
		end := bytes.Index(lower[start:], []byte("</"+name))
		if end < 0 {
			break // unterminated: drop the rest, as a browser would
		}
		closeEnd := bytes.IndexByte(lower[start+end:], '>')
		if closeEnd < 0 {
			break
		}
		i = start + end + closeEnd + 1
	}
	return out.Bytes()
}

// ExtractHTMLText turns HTML bytes into plain text with
// HTMLTextExtractor's rules. It reads only src: nothing is fetched or run.
func ExtractHTMLText(src []byte) (string, error) {
	if !utf8.Valid(src) {
		return "", fmt.Errorf("%w: HTML is not UTF-8; %s does not decode other charsets", ErrInvalid, HTMLTextExtractor)
	}
	dec := xml.NewDecoder(bytes.NewReader(stripRawText(src)))
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity
	var out strings.Builder
	var title strings.Builder
	skip, inTitle := 0, false
	// open is the stack of open element names. Only a title whose parent is
	// head (or html, when head is omitted) is the document title; a title
	// inside svg or anywhere else is not.
	var open []string
	line := func() {
		s := out.String()
		if s != "" && !strings.HasSuffix(s, "\n") {
			out.WriteByte('\n')
		}
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("%w: HTML did not parse: %v", ErrInvalid, err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			if name == "title" && len(open) > 0 && (open[len(open)-1] == "head" || open[len(open)-1] == "html") {
				inTitle = true
			}
			open = append(open, name)
			if skip > 0 || removedElements[name] {
				skip++
				continue
			}
			if blockElements[name] {
				line()
			}
			switch {
			case len(name) == 2 && name[0] == 'h' && name[1] >= '1' && name[1] <= '6':
				out.WriteString(strings.Repeat("#", int(name[1]-'0')) + " ")
			case name == "li":
				out.WriteString("- ")
			}
		case xml.EndElement:
			name := strings.ToLower(t.Name.Local)
			for i := len(open) - 1; i >= 0; i-- {
				if open[i] == name {
					open = open[:i]
					break
				}
			}
			if name == "title" {
				inTitle = false
			}
			if skip > 0 {
				skip--
				continue
			}
			if blockElements[name] {
				line()
			}
		case xml.CharData:
			text := strings.Join(strings.Fields(string(t)), " ")
			if text == "" {
				continue
			}
			if inTitle {
				title.WriteString(text)
				continue
			}
			if skip > 0 {
				continue
			}
			s := out.String()
			if s != "" && !strings.HasSuffix(s, "\n") && !strings.HasSuffix(s, " ") {
				out.WriteByte(' ')
			}
			out.WriteString(text)
		}
	}
	body := strings.TrimSpace(out.String())
	if t := strings.TrimSpace(title.String()); t != "" {
		body = "# " + t + "\n\n" + body
	}
	return body + "\n", nil
}

// htmlInput decides whether a capture's bytes can be extracted as HTML. It
// refuses a body stored with a content coding (gzip and the like: the bytes
// are not HTML until decoded, and this extractor never decodes) and bytes
// that sniff as compressed or binary, whatever the declared type says.
func htmlInput(c Capture, src []byte) error {
	if c.Download != nil {
		if enc := strings.ToLower(strings.TrimSpace(c.Download.Headers["Content-Encoding"])); enc != "" && enc != "identity" {
			return fmt.Errorf("%w: the body is stored with content coding %q; %s does not decode it", ErrInvalid, enc, HTMLTextExtractor)
		}
	}
	sniffed := strings.ToLower(http.DetectContentType(src))
	if !strings.HasPrefix(sniffed, "text/") {
		return fmt.Errorf("%w: the bytes sniff as %s, not text", ErrInvalid, sniffed)
	}
	mt := strings.ToLower(strings.TrimSpace(strings.SplitN(c.MediaType, ";", 2)[0]))
	if mt == "text/html" || mt == "application/xhtml+xml" || strings.HasPrefix(sniffed, "text/html") {
		return nil
	}
	switch extensionOf(c.OriginalName) {
	case "html", "htm", "xhtml":
		return nil
	}
	return fmt.Errorf("%w: not HTML", ErrInvalid)
}

// ExtractText extracts text from an HTML capture's bytes and keeps the
// result as its own object with the extractor version and limits. The
// original is never changed. A failure is recorded and returned, and
// nothing is guessed. Extraction is per object: every capture of the same
// bytes shares it. Repeating it returns the recorded extraction.
//
// The archive lock is held exclusively from the status check to the
// commit, so a quarantine or purge runs entirely before or after (an
// extraction never records output for a deleted source), and a concurrent
// extraction of the same bytes waits and then returns the recorded one
// instead of failing on the extractions primary key.
func (a *Archive) ExtractText(ctx context.Context, captureID string) (Extraction, error) {
	unlock, err := a.lock(ctx, true)
	if err != nil {
		return Extraction{}, err
	}
	defer unlock()
	c, err := a.status(ctx, captureID)
	if err != nil {
		return Extraction{}, err
	}
	// Every per-capture rule (content coding, media type, sniff) runs before
	// a recorded extraction of the same bytes is returned, so the result
	// never depends on which capture of those bytes was extracted first.
	hexSum := strings.TrimPrefix(c.ObjectID, "sha256:")
	src, err := readBounded(a.objectPath(hexSum), MaxObjectBytes)
	if err != nil {
		return Extraction{}, fmt.Errorf("%w: %s: %v", ErrIntegrity, c.ObjectID, err)
	}
	if sum := sha256.Sum256(src); hex.EncodeToString(sum[:]) != hexSum {
		return Extraction{}, fmt.Errorf("%w: %s", ErrIntegrity, c.ObjectID)
	}
	if err := htmlInput(c, src); err != nil {
		return Extraction{}, err
	}
	for _, e := range c.Extractions {
		if e.Extractor == HTMLTextExtractor {
			if e.Status == "failed" {
				// Same outcome as the first run: the failure, not a result.
				return e, fmt.Errorf("%w: recorded %s failure: %s", ErrInvalid, HTMLTextExtractor, e.Detail)
			}
			return e, nil
		}
	}
	text, extractErr := ExtractHTMLText(src)
	e := Extraction{Extractor: HTMLTextExtractor, RecordedAt: a.now().UTC().Format(time.RFC3339Nano)}
	// The registry pool has one connection, so every query outside the
	// transaction runs before it begins; the exclusive lock keeps the
	// results valid until the commit.
	var staged stagedObject
	if extractErr == nil {
		if staged, err = a.stage(strings.NewReader(text)); err != nil {
			return Extraction{}, err
		}
		defer os.Remove(staged.tmpPath)
		if err := a.objectUsable(ctx, staged.objectID); err != nil {
			return Extraction{}, err
		}
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return Extraction{}, err
	}
	defer tx.Rollback()
	var output any
	if extractErr != nil {
		e.Status, e.Detail = "failed", truncateText(extractErr.Error(), 1000)
	} else {
		if err := a.publish(staged); err != nil {
			return Extraction{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO objects (object_id, size, stored_at) VALUES (?, ?, ?)`, staged.objectID, staged.size, e.RecordedAt); err != nil {
			return Extraction{}, err
		}
		e.Status, e.Detail, e.OutputObjectID, e.Size = "succeeded", htmlTextLimits, staged.objectID, staged.size
		output = staged.objectID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO extractions (source_object_id, extractor, status, output_object_id, detail, recorded_at) VALUES (?,?,?,?,?,?)`,
		c.ObjectID, e.Extractor, e.Status, output, e.Detail, e.RecordedAt); err != nil {
		return Extraction{}, err
	}
	if err := tx.Commit(); err != nil {
		return Extraction{}, err
	}
	if extractErr != nil {
		return e, extractErr
	}
	return e, nil
}

func (a *Archive) extractions(ctx context.Context, objectID string) ([]Extraction, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT x.extractor, x.status, COALESCE(x.output_object_id, ''), COALESCE(o.size, 0), x.detail, x.recorded_at FROM extractions x LEFT JOIN objects o ON o.object_id = x.output_object_id WHERE x.source_object_id = ? AND (x.output_object_id IS NULL OR o.state = 'active') ORDER BY x.extractor`, objectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Extraction{}
	for rows.Next() {
		var e Extraction
		if err := rows.Scan(&e.Extractor, &e.Status, &e.OutputObjectID, &e.Size, &e.Detail, &e.RecordedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
