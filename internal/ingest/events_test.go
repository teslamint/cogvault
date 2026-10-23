package ingest

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/teslamint/cogvault/internal/llm"
)

// TestRunOnEventReportsOnlyStateChanges pins which per-file results reach
// RunOptions.OnEvent. The second run is the discriminating case: a refused
// file that is skipped under the same digest profile is still listed as
// "refused" in PerFile, but it is not a new event.
func TestRunOnEventReportsOnlyStateChanges(t *testing.T) {
	m := &mockLLM{fn: func(req llm.DigestRequest) (*llm.DigestResult, error) {
		switch filepath.Base(req.SourcePath) {
		case "a.md":
			return nil, llm.ErrRefused
		case "c.md":
			return nil, errPermanent
		}
		return &llm.DigestResult{PageContent: validPage(req.PageSlug)}, nil
	}}
	h := newHarness(t, []string{"md"}, m)
	a := h.write(t, "a.md", "one")
	b := h.write(t, "b.md", "two")
	c := h.write(t, "c.md", "three")
	h.write(t, "x.txt", "not allowed")

	var events []FileResult
	record := func(f FileResult) { events = append(events, f) }

	first, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive", OnEvent: record})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	wantActions := [][2]string{{actionRefused, a}, {actionDigested, b}, {actionFailed, c}}
	assertEvents(t, events, wantActions)
	for _, ev := range events {
		if !containsResult(first.PerFile, ev) {
			t.Errorf("event %+v is missing from PerFile %+v", ev, first.PerFile)
		}
	}

	events = nil
	second, err := h.runner.Run(context.Background(), RunOptions{Origin: "interactive", OnEvent: record})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if !containsResult(second.PerFile, FileResult{Path: a, Action: actionRefused, Error: resultError(first, a)}) {
		t.Fatalf("fixture: refused file not listed as skipped-refused in second run: %+v", second.PerFile)
	}
	assertEvents(t, events, [][2]string{{actionFailed, c}})
}

func assertEvents(t *testing.T, got []FileResult, want [][2]string) {
	t.Helper()
	var gotPairs [][2]string
	for _, ev := range got {
		gotPairs = append(gotPairs, [2]string{ev.Action, ev.Path})
	}
	if !reflect.DeepEqual(gotPairs, want) {
		t.Fatalf("events = %v, want %v", gotPairs, want)
	}
	for _, ev := range got {
		if ev.Action != actionDigested && strings.TrimSpace(ev.Error) == "" {
			t.Errorf("event %+v has no error text", ev)
		}
	}
}

func containsResult(results []FileResult, want FileResult) bool {
	for _, r := range results {
		if r == want {
			return true
		}
	}
	return false
}

func resultError(report *Report, path string) string {
	for _, r := range report.PerFile {
		if r.Path == path {
			return r.Error
		}
	}
	return ""
}
