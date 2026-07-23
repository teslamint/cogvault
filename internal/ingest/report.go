package ingest

import (
	"fmt"
	"strings"
)

const (
	actionDigested    = "digested"
	actionWouldDigest = "would-digest"
	actionFailed      = "failed"
	actionRefused     = "refused"
	actionSkipped     = "skipped"
	actionDeferred    = "deferred"
	actionExhausted   = "exhausted"
	actionSourceError = "source-error"
)

type FileResult struct {
	Path   string
	Action string
	Error  string
}

type Report struct {
	Digested     int
	Failed       int
	Refused      int
	Skipped      int
	Deferred     int
	Unchanged    int
	SourceErrors int
	PerFile      []FileResult
}

func (r *Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "digested=%d failed=%d refused=%d skipped=%d deferred=%d unchanged=%d source-errors=%d\n",
		r.Digested, r.Failed, r.Refused, r.Skipped, r.Deferred, r.Unchanged, r.SourceErrors)
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
