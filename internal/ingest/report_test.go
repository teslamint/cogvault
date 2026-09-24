package ingest

import "testing"

func TestReportSummary(t *testing.T) {
	const counts = "scanned=5 digested=2 failed=1 refused=0 skipped=1 deferred=0 unchanged=1 archived=3 source-errors=0"

	t.Run("happy", func(t *testing.T) {
		r := &Report{
			Scanned:      5,
			Digested:     2,
			Failed:       1,
			Skipped:      1,
			Unchanged:    1,
			Archived:     3,
			NotExamined:  0,
			SourceErrors: 0,
		}
		if got := r.Summary(); got != counts {
			t.Fatalf("Summary() = %q, want %q", got, counts)
		}
	})

	t.Run("edge not-examined present", func(t *testing.T) {
		r := &Report{
			Scanned:      5,
			Digested:     2,
			Failed:       1,
			Skipped:      1,
			Unchanged:    1,
			Archived:     3,
			SourceErrors: 0,
			NotExamined:  4,
		}
		want := counts + " not-examined=4"
		if got := r.Summary(); got != want {
			t.Fatalf("Summary() = %q, want %q", got, want)
		}
	})

	t.Run("edge not-examined absent when zero", func(t *testing.T) {
		r := &Report{Scanned: 1, Digested: 1, NotExamined: 0}
		want := "scanned=1 digested=1 failed=0 refused=0 skipped=0 deferred=0 unchanged=0 archived=0 source-errors=0"
		if got := r.Summary(); got != want {
			t.Fatalf("Summary() = %q, want %q", got, want)
		}
	})
}

func TestReportStringStartsWithSummary(t *testing.T) {
	r := &Report{
		Scanned:      5,
		Digested:     2,
		Failed:       1,
		Skipped:      1,
		Unchanged:    1,
		Archived:     3,
		SourceErrors: 0,
		SumMismatch:  "report sum mismatch: scanned=5 sum=4",
		PerFile: []FileResult{
			{Path: "a.md", Action: actionDigested},
			{Path: "b.md", Action: actionFailed, Error: "boom"},
		},
	}

	want := "scanned=5 digested=2 failed=1 refused=0 skipped=1 deferred=0 unchanged=1 archived=3 source-errors=0\n" +
		"  !! report sum mismatch: scanned=5 sum=4\n" +
		"  digested  a.md\n" +
		"  failed    b.md  boom\n"

	if got := r.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}

	summary := r.Summary()
	body := r.String()
	if len(body) < len(summary)+1 || body[:len(summary)] != summary || body[len(summary)] != '\n' {
		t.Fatalf("String() does not start with Summary()+\"\\n\": summary=%q body=%q", summary, body)
	}
}
