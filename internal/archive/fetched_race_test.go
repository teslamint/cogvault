package archive

import (
	"errors"
	"strings"
	"testing"
)

// TestDerivationIsNotRecordedForADeletedBody covers the window in
// ImportFetched between committing the body capture and recording the fetch
// file: a quarantine there must stop the derivation, or the fetch file (which
// holds the same bytes) would stay active.
func TestDerivationIsNotRecordedForADeletedBody(t *testing.T) {
	a, _ := openTest(t)
	body := mustCapture(t, a, source("response body\n", "https://example.org/r"))
	quarantine(t, a, body.ObjectID)
	staged, err := a.stage(strings.NewReader("---\nurl: https://example.org/r\n---\nresponse body\n"))
	if err != nil {
		t.Fatal(err)
	}
	err = a.addDerivation(ctx, staged, FetchedRequest{DerivedName: "r.md", FetchedAt: "2026-01-01T00:00:00Z"}, body.CaptureID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("derivation for a quarantined body: err = %v", err)
	}
	if ids, _ := a.ids(ctx, `SELECT derived_object_id FROM derivations`); len(ids) != 0 {
		t.Fatalf("derivation recorded: %v", ids)
	}
}
