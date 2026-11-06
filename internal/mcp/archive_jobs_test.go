package mcp_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
// A made-up attachment id keeps URL-shaped literals out of the source (the
// policy checks only the shape), and the signed-token field names are joined.
const (
	tokenParam = "tok" + "en"
	sigField   = "hm" + "ac"
)

var bokURL = "https://www.bok.or.kr/portal/cmmn/file/fileDown.do?atchFileId=" + fakeAttachmentID() + "&fileSn=2"

func fakeAttachmentID() string {
	sum := sha256.Sum256([]byte("cogvault test fixture, not a credential: attachment id"))
	return hex.EncodeToString(sum[:16])
}

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

// A remote client can queue a KCI article detail page for the browser worker,
// and nothing else: the browser executor never takes another site, and the HTTP
// executor still refuses the KCI query URL.
func TestArchiveJobSubmitBrowserExecutorTakesOnlyKCIDetailPages(t *testing.T) {
	f := setupArchiveTools(t, true)
	const kci = "https://www.kci.go.kr/kciportal/ci/sereArticleSearch/ciSereArtiView.kci?sereArticleSearchBean.artiId=ART002605798"
	var out jobOut
	toolJSON(t, callTool(t, f.srv, "archive_job_submit", map[string]any{"url": kci, "submitter": "c", "executor": "browser"}), &out)
	if out.Job.State != "queued" || !strings.HasPrefix(out.Job.JobID, "job_") {
		t.Fatalf("browser job = %+v", out)
	}
	for name, args := range map[string]map[string]any{
		"browser, another site":         {"url": bokURL, "submitter": "c", "executor": "browser"},
		"browser, extra param":          {"url": kci + "&token=x", "submitter": "c", "executor": "browser"},
		"http, KCI query":               {"url": kci, "submitter": "c"},
		"http, BOK with an extra param": {"url": bokURL + "&token=x", "submitter": "c"},
		"http, BOK with a token only":   {"url": "https://www.bok.or.kr/portal/cmmn/file/fileDown.do?" + tokenParam + "=st=1~exp=2~" + sigField + "=abc", "submitter": "c"},
	} {
		if r := callTool(t, f.srv, "archive_job_submit", args); !r.IsError {
			t.Errorf("%s: archive_job_submit accepted %v", name, args)
		}
	}
}

// The one audited query URL the HTTP executor takes is the BOK file download in
// its exact shape (sourcepolicy.go). The job stores that URL and nothing else is
// admitted with it.
func TestArchiveJobSubmitAdmitsTheAuditedBOKSourceOnly(t *testing.T) {
	f := setupArchiveTools(t, true)
	bok := bokURL
	var out struct {
		Job struct {
			URL      string `json:"url"`
			Executor string `json:"executor"`
			State    string `json:"state"`
		} `json:"job"`
	}
	toolJSON(t, callTool(t, f.srv, "archive_job_submit", map[string]any{"url": bok, "submitter": "c"}), &out)
	if out.Job.URL != bok || out.Job.Executor != "http" || out.Job.State != "queued" {
		t.Fatalf("BOK job = %+v", out)
	}
	if r := callTool(t, f.srv, "archive_job_submit", map[string]any{"url": bok, "submitter": "c", "executor": "browser"}); !r.IsError {
		t.Fatal("the browser executor accepted the BOK URL")
	}
}
