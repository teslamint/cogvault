package trialmcp

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/teslamint/cogvault/internal/archive"
	"github.com/teslamint/cogvault/internal/trial"
	"time"
)

func fixture(t *testing.T) *trial.Service {
	t.Helper()
	cfg, err := trial.FromEnviron([]string{trial.EnvRoot + "=" + filepath.Join(t.TempDir(), "trial"), trial.EnvAck + "=" + trial.AckPhrase, trial.EnvAcceptedBy + "=test", trial.EnvAllowedHosts + "=www.rfc-editor.org", trial.EnvMaxQueued + "=2", trial.EnvMaxSubmitsPerDay + "=2", trial.EnvMaxFetchesHour + "=1", trial.EnvMaxFetchesDay + "=2", trial.EnvMinInterval + "=30s", trial.EnvMaxAttempts + "=2"})
	if err != nil {
		t.Fatal(err)
	}
	s, err := trial.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func call(t *testing.T, s *trial.Service, name string, args any) string {
	t.Helper()
	srv := NewServer(s)
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	res := srv.HandleMessage(context.Background(), raw)
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func TestInventoryAndStrictInput(t *testing.T) {
	s := fixture(t)
	tools := NewServer(s).ListTools()
	if len(tools) != 4 {
		t.Fatalf("tools: %v", tools)
	}
	for _, name := range []string{"trial_submit", "trial_status", "trial_import_manifest", "trial_report_status"} {
		v, ok := tools[name]
		if !ok {
			t.Fatal(name)
		}
		if v.Tool.InputSchema.AdditionalProperties != false {
			t.Fatal("extra parameters allowed")
		}
		key := "url"
		if name == "trial_status" {
			key = "job_id"
		}
		if name == "trial_import_manifest" {
			key = "manifest"
		}
		if name == "trial_report_status" {
			key = "report_id"
		}
		wantFields := 1
		if name == "trial_submit" {
			wantFields = 3
		}
		if len(v.Tool.InputSchema.Properties) != wantFields || v.Tool.InputSchema.Properties[key] == nil {
			t.Fatalf("unexpected schema: %+v", v.Tool.InputSchema)
		}
		if name == "trial_submit" && (len(v.Tool.InputSchema.Required) != 1 || v.Tool.InputSchema.Required[0] != "url") {
			t.Fatal("submit URL not required")
		}
		if name == "trial_status" && len(v.Tool.InputSchema.Required) != 0 {
			t.Fatal("status ID must be optional")
		}

	}
	for _, args := range []any{map[string]any{"url": "https://www.rfc-editor.org/rfc/rfc9110.pdf", "executor": "browser"}, map[string]any{"url": 23}, map[string]any{"url": strings.Repeat("x", 2049)}, []any{"url"}} {
		if got := call(t, s, "trial_submit", args); !strings.Contains(got, `"isError":true`) && !strings.Contains(got, `"error"`) {
			t.Fatal(got)
		}
	}
	for _, u := range []string{"https://example.com/a.pdf", "http://www.rfc-editor.org/a.pdf", "https://u:p@www.rfc-editor.org/a.pdf", "https://www.rfc-editor.org/a.pdf?secret=x", "https://www.rfc-editor.org/a.pdf#secret"} {
		if got := call(t, s, "trial_submit", map[string]any{"url": u}); !strings.Contains(got, "request_refused") {
			t.Fatal(got)
		}
	}
}
func TestSubmitDedupeAndLimits(t *testing.T) {
	s := fixture(t)
	args := map[string]any{"url": "https://www.rfc-editor.org/rfc/rfc9110.pdf"}
	if got := call(t, s, "trial_submit", args); !strings.Contains(got, `\"existing\":false`) {
		t.Fatal(got)
	}
	if got := call(t, s, "trial_submit", args); !strings.Contains(got, `\"existing\":true`) {
		t.Fatal(got)
	}
	call(t, s, "trial_submit", map[string]any{"url": "https://www.rfc-editor.org/rfc/rfc9111.pdf"})
	if got := call(t, s, "trial_submit", args); !strings.Contains(got, "limit_reached") {
		t.Fatal(got)
	}
	if got := call(t, s, "trial_status", map[string]any{}); !strings.Contains(got, `\"waiting_or_running\":2`) {
		t.Fatal(got)
	}
}
func TestInfrastructureErrorSanitized(t *testing.T) {
	s := fixture(t)
	_ = s.Close()
	got := call(t, s, "trial_status", map[string]any{})
	if !strings.Contains(got, "temporarily_unavailable") || strings.Contains(got, s.Cfg.Root) || strings.Contains(got, "database") {
		t.Fatal(got)
	}
}

func TestJobAndCaptureProjection(t *testing.T) {
	s := fixture(t)
	ctx := context.Background()
	j, _, err := s.A.SubmitJob(ctx, archive.JobRequest{URL: "https://unapproved.example/secret.pdf", Submitter: "SECRET label", MaxAttempts: 2})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.A.ClaimJob(ctx, archive.ExecutorHTTP, "SECRET-host-PID", time.Minute)
	if err != nil || claimed == nil {
		t.Fatal(err)
	}
	c, err := s.A.CaptureSource(ctx, archive.SourceRequest{Content: strings.NewReader("%PDF-fixture"), OriginalName: "/SECRET/path", Supplier: "SECRET supplier", Title: "SECRET title", MediaType: "SECRET media"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.A.CompleteJob(ctx, j.JobID, "SECRET-host-PID", claimed.AttemptsMade, archive.JobOutcome{State: archive.JobHeld, CaptureID: c.CaptureID, Detail: "SECRET detail /private/path", Response: `{"headers":{"Authorization":"SECRET"}}`})
	if err != nil {
		t.Fatal(err)
	}
	got := call(t, s, "trial_status", map[string]any{"job_id": j.JobID})
	for _, bad := range []string{"SECRET", "unapproved", "headers", "lease_owner", "original_name", "ingest"} {
		if strings.Contains(got, bad) {
			t.Fatalf("leaked %s: %s", bad, got)
		}
	}
	for _, want := range []string{c.ObjectID, `\"state\":\"held\"`, `\"completeness\":\"unknown\"`, `\"size\":12`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s: %s", want, got)
		}
	}
}
func TestUnknownProjectionAndInvalidStatus(t *testing.T) {
	v := viewJob(archive.Job{JobID: "SECRET", State: "SECRET", URL: "SECRET"})
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "SECRET") {
		t.Fatal(string(b))
	}
	c := viewCapture(archive.Capture{ObjectID: "SECRET", Completeness: "SECRET"})
	b, _ = json.Marshal(c)
	if strings.Contains(string(b), "SECRET") {
		t.Fatal(string(b))
	}
	s := fixture(t)
	for _, args := range []any{map[string]any{"job_id": "../SECRET"}, map[string]any{"job_id": nil}, map[string]any{"limit": 100}, []any{}} {
		got := call(t, s, "trial_status", args)
		if !strings.Contains(got, "invalid_arguments") && !strings.Contains(got, `"error"`) {
			t.Fatal(got)
		}
	}
	got := call(t, s, "trial_status", map[string]any{"job_id": "job_00000000000000000000000000000000"})
	if !strings.Contains(got, "job_not_found") {
		t.Fatal(got)
	}
}
