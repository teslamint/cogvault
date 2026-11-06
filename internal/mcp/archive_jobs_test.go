package mcp_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/teslamint/cogvault/internal/archive"
)

type jobOut struct {
	Job struct {
		JobID           string `json:"job_id"`
		State           string `json:"state"`
		Submitter       string `json:"submitter"`
		ClaimRef        string `json:"claim_ref"`
		MaxAttempts     int    `json:"max_attempts"`
		ResultCaptureID string `json:"result_capture_id"`
		Attempts        []struct {
			Outcome string `json:"outcome"`
			Detail  string `json:"detail"`
		} `json:"attempts"`
	} `json:"job"`
	Existing bool `json:"existing"`
}

// The job tools sit behind the same gate as the capture tools: a closed gate
// queues nothing and reads nothing.
func TestArchiveJobToolsRefuseWhileGateIsClosed(t *testing.T) {
	f := setupArchiveTools(t, false)
	for name, args := range map[string]map[string]any{
		"archive_job_submit": {"url": "https://example.org/a.pdf", "submitter": "c"},
		"archive_job_status": {"job_id": "job_x"},
		"archive_job_retry":  {"job_id": "job_x"},
	} {
		r := callTool(t, f.srv, name, args)
		if !r.IsError || !strings.Contains(extractText(t, r), "disabled") {
			t.Fatalf("%s with a closed gate: %+v", name, r)
		}
	}
	// The closed-gate submit queued nothing: the same request is new.
	_, existing, err := f.archive.SubmitJob(context.Background(), archive.JobRequest{URL: "https://example.org/a.pdf", Submitter: "c"})
	if err != nil || existing {
		t.Fatalf("SubmitJob after the closed-gate call: existing=%v err=%v", existing, err)
	}
}

// One client submits and another reads the job by ID with no shared state
// except the archive. A repeated request returns the same job. The remote
// view carries no worker name, no acquisition record, and no resolver or
// socket address, even when the stored attempt held them.
func TestArchiveJobSubmitStatusAcrossClients(t *testing.T) {
	f := setupArchiveTools(t, true)
	var first jobOut
	toolJSON(t, callTool(t, f.srv, "archive_job_submit", map[string]any{"url": "https://example.org/a.pdf", "submitter": "claude-chat", "claim": "p. 4", "edition": "2nd ed."}), &first)
	if !strings.HasPrefix(first.Job.JobID, "job_") || first.Job.State != "queued" || first.Existing || first.Job.MaxAttempts != 5 {
		t.Fatalf("submit = %+v", first)
	}
	var again jobOut
	toolJSON(t, callTool(t, f.srv, "archive_job_submit", map[string]any{"url": "https://example.org/a.pdf", "submitter": "chatgpt", "claim": "p. 4", "edition": "2nd ed."}), &again)
	if !again.Existing || again.Job.JobID != first.Job.JobID || again.Job.Submitter != "claude-chat" {
		t.Fatalf("second client = %+v", again)
	}

	ctx := context.Background()
	if j, err := f.archive.ClaimJob(ctx, archive.ExecutorHTTP, "nas-host-4242", time.Minute); err != nil || j == nil {
		t.Fatalf("claim = %+v, %v", j, err)
	}
	if _, err := f.archive.CompleteJob(ctx, first.Job.JobID, "nas-host-4242", 1, archive.JobOutcome{
		State: archive.JobRetry, Detail: "network error: timed out",
		Response: `{"hops":[{"url":"https://example.org/a.pdf","address":"10.9.8.7:443","status":0}]}`,
	}); err != nil {
		t.Fatal(err)
	}
	r := callTool(t, f.srv, "archive_job_status", map[string]any{"job_id": first.Job.JobID})
	raw := extractText(t, r)
	for _, leak := range []string{"nas-host-4242", "10.9.8.7", "lease_owner", "response", f.aroot} {
		if strings.Contains(raw, leak) {
			t.Fatalf("archive_job_status exposes %q: %s", leak, raw)
		}
	}
	var got jobOut
	toolJSON(t, r, &got.Job)
	if got.Job.State != "queued" || len(got.Job.Attempts) != 1 || got.Job.Attempts[0].Outcome != "retry" || got.Job.Attempts[0].Detail != "network error: timed out" || got.Job.ClaimRef != "p. 4" {
		t.Fatalf("status = %+v", got.Job)
	}
}

// A URL the acquirer would refuse never enters the queue, undeclared and
// mistyped arguments are refused, and a repeated key for another URL is a
// conflict.
func TestArchiveJobSubmitRefusals(t *testing.T) {
	f := setupArchiveTools(t, true)
	for name, args := range map[string]map[string]any{
		"query":         {"url": "https://example.org/a?token=1", "submitter": "c"},
		"userinfo":      {"url": "https://u:p@example.org/a", "submitter": "c"},
		"file scheme":   {"url": "file:///etc/passwd", "submitter": "c"},
		"no submitter":  {"url": "https://example.org/a"},
		"bad digest":    {"url": "https://example.org/a", "submitter": "c", "seen_sha256": "xyz"},
		"bad executor":  {"url": "https://example.org/a", "submitter": "c", "executor": "shell"},
		"unknown param": {"url": "https://example.org/a", "submitter": "c", "max_attempts": "99"},
		"wrong type":    {"url": 5, "submitter": "c"},
		"report path":   {"url": "https://example.org/a", "submitter": "c", "report_capture_id": "../../etc/passwd"},
	} {
		if r := callTool(t, f.srv, "archive_job_submit", args); !r.IsError {
			t.Errorf("%s: archive_job_submit accepted %v", name, args)
		}
	}
	callTool(t, f.srv, "archive_job_submit", map[string]any{"url": "https://example.org/k1", "submitter": "c", "key": "same"})
	if r := callTool(t, f.srv, "archive_job_submit", map[string]any{"url": "https://example.org/k2", "submitter": "c", "key": "same"}); !r.IsError || !strings.Contains(extractText(t, r), "different content") {
		t.Fatalf("key reuse = %+v", r)
	}
	var sub jobOut
	toolJSON(t, callTool(t, f.srv, "archive_job_submit", map[string]any{"url": "https://example.org/ok", "submitter": "c"}), &sub)
	if sub.Existing {
		t.Fatal("a refused submission left a job behind that a valid one matched")
	}
}

// Retry requeues only a job that ended without bytes (or held ones), and a
// missing job is an ordinary error.
func TestArchiveJobRetryTool(t *testing.T) {
	f := setupArchiveTools(t, true)
	var sub jobOut
	toolJSON(t, callTool(t, f.srv, "archive_job_submit", map[string]any{"url": "https://example.org/a.pdf", "submitter": "c"}), &sub)
	if r := callTool(t, f.srv, "archive_job_retry", map[string]any{"job_id": sub.Job.JobID}); !r.IsError || !strings.Contains(extractText(t, r), "not in a state") {
		t.Fatalf("retry of a queued job = %+v", r)
	}
	ctx := context.Background()
	if _, err := f.archive.ClaimJob(ctx, archive.ExecutorHTTP, "w", time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := f.archive.CompleteJob(ctx, sub.Job.JobID, "w", 1, archive.JobOutcome{State: archive.JobHuman, Detail: "access restricted"}); err != nil {
		t.Fatal(err)
	}
	var retried struct {
		State       string `json:"state"`
		MaxAttempts int    `json:"max_attempts"`
	}
	toolJSON(t, callTool(t, f.srv, "archive_job_retry", map[string]any{"job_id": sub.Job.JobID}), &retried)
	if retried.State != "queued" || retried.MaxAttempts != 6 {
		t.Fatalf("retry = %+v", retried)
	}
	if r := callTool(t, f.srv, "archive_job_status", map[string]any{"job_id": "job_00000000000000000000000000000000"}); !r.IsError || !strings.Contains(extractText(t, r), "job not found") {
		t.Fatalf("missing job = %+v", r)
	}
}

// A registry failure reaches the client as a fixed message, with no SQLite
// text or path.
func TestArchiveJobToolInternalErrorsAreGeneric(t *testing.T) {
	f := setupArchiveTools(t, true)
	f.archive.Close()
	for name, args := range map[string]map[string]any{
		"archive_job_submit": {"url": "https://example.org/a.pdf", "submitter": "c"},
		"archive_job_status": {"job_id": "job_x"},
		"archive_job_retry":  {"job_id": "job_x"},
	} {
		r := callTool(t, f.srv, name, args)
		msg := extractText(t, r)
		if !r.IsError || !strings.Contains(msg, "internal archive error") || strings.Contains(msg, "sql") || strings.Contains(msg, f.aroot) {
			t.Fatalf("%s = %q", name, msg)
		}
	}
}
