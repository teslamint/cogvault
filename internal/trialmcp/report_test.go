package trialmcp

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestManifestMCPNoFetchManualStatusAndStrictSurface(t *testing.T) {
	s := fixture(t)
	manifest := `{"version":1,"report_id":"report-fixture","selected_sha256":"sha256:` + strings.Repeat("a", 64) + `","sources":[{"id":"S0001","citation_url":"https://www.rfc-editor.org/rfc/rfc9110.pdf","relation":"citation","citation_count":1,"mode":"manual"}]}`
	for _, name := range []string{"trial_import_manifest", "trial_report_status"} {
		if got := call(t, s, name, map[string]any{"file": "/private/not-allowed"}); !strings.Contains(got, "invalid_arguments") {
			t.Fatalf("filesystem parameter accepted: %s", got)
		}
	}
	if got := call(t, s, "trial_import_manifest", map[string]any{"manifest": manifest}); !strings.Contains(got, "capture_id") || strings.Contains(got, `"isError":true`) {
		t.Fatalf("import: %s", got)
	}
	if got := call(t, s, "trial_import_manifest", map[string]any{"manifest": manifest}); !strings.Contains(got, `\"existing\":true`) {
		t.Fatalf("dedupe: %s", got)
	}
	if got := call(t, s, "trial_report_status", map[string]any{"report_id": "report-fixture"}); !strings.Contains(got, "S0001") || strings.Contains(got, `"isError":true`) {
		t.Fatalf("report status: %s", got)
	}
	if got := call(t, s, "trial_submit", map[string]any{"url": "https://www.rfc-editor.org/rfc/rfc9110.pdf", "report_id": "report-fixture", "source_id": "S0001"}); !strings.Contains(got, "request_refused") {
		t.Fatalf("manual source submitted: %s", got)
	}
	if got := call(t, s, "trial_submit", map[string]any{"url": "https://www.rfc-editor.org/rfc/rfc9110.pdf", "report_id": "report-fixture"}); !strings.Contains(got, "invalid_arguments") {
		t.Fatalf("unpaired source: %s", got)
	}
	a, err := s.A.JobActivity(context.Background(), time.Time{})
	if err != nil || a.Created != 0 || a.Fetches != 0 {
		t.Fatalf("import/manual path queued or fetched: %+v %v", a, err)
	}
}

func TestBudgetNamesAttemptUnitsAndHTTPUpperBound(t *testing.T) {
	s := fixture(t)
	got := call(t, s, "trial_status", map[string]any{})
	for _, want := range []string{"job_attempts", "attempts_last_hour", "attempts_last_day", "max_http_requests_per_attempt", "requests_last_hour"} {
		if !strings.Contains(got, want) {
			t.Fatalf("budget omitted %s: %s", want, got)
		}
	}
}
