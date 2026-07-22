package ingest

import (
	"fmt"
	"strings"
)

const (
	actionDigested    = "digested"
	actionWouldDigest = "would-digest"
	actionFailed      = "failed"
	actionSkipped     = "skipped"
	actionDeferred    = "deferred"
	actionExhausted   = "exhausted"
)

type FileResult struct {
	Path   string
	Action string
	Error  string
}

type Report struct {
	Digested  int
	Failed    int
	Skipped   int
	Deferred  int
	Unchanged int
	PerFile   []FileResult
}

func (r *Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "digested=%d failed=%d skipped=%d deferred=%d unchanged=%d\n",
		r.Digested, r.Failed, r.Skipped, r.Deferred, r.Unchanged)
	width := 0
	for _, f := range r.PerFile {
		if len(f.Action) > width {
			width = len(f.Action)
		}
	}
	for _, f := range r.PerFile {
		line := fmt.Sprintf("  %-*s  %s", width, f.Action, f.Path)
		if f.Error != "" {
			line += "  " + f.Error
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}
