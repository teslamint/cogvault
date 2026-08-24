package ingest

import (
	"fmt"
	"strings"
)

const (
	actionDigested     = "digested"
	actionWouldDigest  = "would-digest"
	actionFailed       = "failed"
	actionRefused      = "refused"
	actionSkipped      = "skipped"
	actionDeferred     = "deferred"
	actionExhausted    = "exhausted"
	actionSourceError  = "source-error"
	actionArchived     = "archived"
	actionWouldArchive = "would-archive"
)

type FileResult struct {
	Path   string
	Action string
	Error  string
}

type Report struct {
	Scanned      int
	Digested     int
	Failed       int
	Refused      int
	Skipped      int
	Deferred     int
	Unchanged    int
	Archived     int
	SourceErrors int
	NotExamined  int
	SumMismatch  string
	PerFile      []FileResult
	NewAttention []FileResult
}

func (r *Report) SumCheck() error {
	sum := r.Digested + r.Failed + r.Refused + r.Skipped + r.Deferred + r.Unchanged + r.NotExamined
	if sum != r.Scanned {
		return fmt.Errorf("report sum mismatch: scanned=%d sum=%d (digested=%d failed=%d refused=%d skipped=%d deferred=%d unchanged=%d not-examined=%d)",
			r.Scanned, sum, r.Digested, r.Failed, r.Refused, r.Skipped, r.Deferred, r.Unchanged, r.NotExamined)
	}
	return nil
}

func (r *Report) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "scanned=%d digested=%d failed=%d refused=%d skipped=%d deferred=%d unchanged=%d archived=%d source-errors=%d",
		r.Scanned, r.Digested, r.Failed, r.Refused, r.Skipped, r.Deferred, r.Unchanged, r.Archived, r.SourceErrors)
	if r.NotExamined > 0 {
		fmt.Fprintf(&b, " not-examined=%d", r.NotExamined)
	}
	b.WriteByte('\n')
	if r.SumMismatch != "" {
		fmt.Fprintf(&b, "  !! %s\n", r.SumMismatch)
	}
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
