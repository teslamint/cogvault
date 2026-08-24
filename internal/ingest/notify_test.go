package ingest

import (
	"bytes"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
)

func TestNotifyOneEntry(t *testing.T) {
	var title, body string
	runner := &Runner{notifyFunc: func(gotTitle, gotBody string) error {
		title, body = gotTitle, gotBody
		return nil
	}}
	report := &Report{NewAttention: []FileResult{{
		Path:  filepath.Join("sources", "report.pdf"),
		Error: "validate: invalid page",
	}}}

	runner.Notify(report)

	if title != "cogvault ingest" {
		t.Fatalf("title = %q, want %q", title, "cogvault ingest")
	}
	if body != "1건 주의 필요 — report.pdf (invalid page)" {
		t.Fatalf("body = %q", body)
	}
}

func TestNotifyMultipleEntries(t *testing.T) {
	var body string
	runner := &Runner{notifyFunc: func(_, gotBody string) error {
		body = gotBody
		return nil
	}}
	report := &Report{NewAttention: []FileResult{
		{Path: filepath.Join("sources", "first.pdf"), Error: "digest: policy refusal"},
		{Path: filepath.Join("sources", "second.pdf"), Error: "validate: invalid page"},
		{Path: filepath.Join("sources", "third.pdf"), Error: "validate: invalid page"},
	}}

	runner.Notify(report)

	want := "3건 주의 필요 — first.pdf (policy refusal) 외 2건"
	if body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

func TestNotifyEmptyAttentionDoesNotInvokeNotifier(t *testing.T) {
	called := false
	runner := &Runner{notifyFunc: func(_, _ string) error {
		called = true
		return nil
	}}

	runner.Notify(&Report{})

	if called {
		t.Fatal("notifyFunc called for empty NewAttention")
	}
}

func TestNotifyExtractsAndTruncatesErrorPrefix(t *testing.T) {
	detail := "llm.Digest request: " + strings.Repeat("x", 50)
	var body string
	runner := &Runner{notifyFunc: func(_, gotBody string) error {
		body = gotBody
		return nil
	}}
	report := &Report{NewAttention: []FileResult{{
		Path:  "source.md",
		Error: "digest:   " + detail,
	}}}

	runner.Notify(report)

	wantDetail := string([]rune(detail)[:60])
	want := "1건 주의 필요 — source.md (" + wantDetail + ")"
	if body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

func TestNotifyWithoutColonUsesFullError(t *testing.T) {
	var body string
	runner := &Runner{notifyFunc: func(_, gotBody string) error {
		body = gotBody
		return nil
	}}

	runner.Notify(&Report{NewAttention: []FileResult{{Path: "source.md", Error: "policy refusal"}}})

	want := "1건 주의 필요 — source.md (policy refusal)"
	if body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
}

func TestNotifyErrorLogsWarning(t *testing.T) {
	var logs bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })
	runner := &Runner{notifyFunc: func(_, _ string) error {
		return errors.New("notifier unavailable")
	}}

	runner.Notify(&Report{NewAttention: []FileResult{{Path: "source.md", Error: "digest: failure"}}})

	if !strings.Contains(logs.String(), "WARN") || !strings.Contains(logs.String(), "notifier unavailable") {
		t.Fatalf("warning log = %q", logs.String())
	}
}

func TestNewlyExhaustedNotifies(t *testing.T) {
	h := newHarness(t, []string{"md"}, okLLM())
	h.runner.cfg.LLM.Model = "current-model"
	var body string
	h.runner.notifyFunc = func(_, gotBody string) error {
		body = gotBody
		return nil
	}
	report := &Report{}
	entry := scanEntry{absPath: filepath.Join(h.srcDir, "exhausted.md"), sourceDir: h.srcDir}
	prev := &ledgerRow{attempts: maxAttempts - 1, llmModel: "current-model"}

	h.runner.recordFailure(entry, "hash", "scheduled", prev, report, "validate: invalid page", classPermanent)
	h.runner.Notify(report)

	want := "1건 주의 필요 — exhausted.md (invalid page)"
	if body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
}
