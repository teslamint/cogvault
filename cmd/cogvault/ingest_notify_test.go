package main

import (
	"errors"
	"testing"

	"github.com/teslamint/cogvault/internal/ingest"
)

func TestRunIngestPassesNotificationGateInputs(t *testing.T) {
	for _, tt := range []struct {
		name          string
		scheduledFlag []string
		wantScheduled bool
	}{
		{name: "scheduled", scheduledFlag: []string{"--scheduled"}, wantScheduled: true},
		{name: "interactive", wantScheduled: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fakeClaudeOnPath(t)
			t.Setenv("CLAUDE_FAKE_MODE", "refusal_exit0")
			configPath, srcDir, _, _ := setupIngestVault(t)
			writeAgedSource(t, srcDir, "attention.pdf", "content")

			original := runIngestNotify
			t.Cleanup(func() { runIngestNotify = original })
			called := false
			runIngestNotify = func(_ reportNotifier, report *ingest.Report, scheduled bool, runErr error) {
				called = true
				if runErr != nil {
					t.Errorf("runErr = %v, want nil", runErr)
				}
				if scheduled != tt.wantScheduled {
					t.Errorf("scheduled = %v, want %v", scheduled, tt.wantScheduled)
				}
				if report == nil || len(report.NewAttention) != 1 {
					t.Errorf("report = %+v, want one new attention item", report)
				}
			}

			args := []string{"ingest", "--config", configPath}
			args = append(args, tt.scheduledFlag...)
			if _, _, err := executeCommand(args...); err != nil {
				t.Fatalf("ingest failed: %v", err)
			}
			if !called {
				t.Fatal("runIngest did not call the notification gate")
			}
		})
	}
}

type capturingReportNotifier struct {
	calls  int
	report *ingest.Report
}

func (n *capturingReportNotifier) Notify(report *ingest.Report) {
	n.calls++
	n.report = report
}

func TestNotifyAfterRunScheduledSuccess(t *testing.T) {
	notifier := &capturingReportNotifier{}
	report := &ingest.Report{}

	notifyAfterRun(notifier, report, true, nil)

	if notifier.calls != 1 || notifier.report != report {
		t.Fatalf("notifier = {calls:%d report:%p}, want {calls:1 report:%p}", notifier.calls, notifier.report, report)
	}
}

func TestNotifyAfterRunInteractiveSuccess(t *testing.T) {
	notifier := &capturingReportNotifier{}

	notifyAfterRun(notifier, &ingest.Report{}, false, nil)

	if notifier.calls != 0 {
		t.Fatalf("calls = %d, want 0", notifier.calls)
	}
}

func TestNotifyAfterRunNilReport(t *testing.T) {
	notifier := &capturingReportNotifier{}

	notifyAfterRun(notifier, nil, true, nil)

	if notifier.calls != 0 {
		t.Fatalf("calls = %d, want 0", notifier.calls)
	}
}

func TestNotifyAfterRunError(t *testing.T) {
	notifier := &capturingReportNotifier{}

	notifyAfterRun(notifier, &ingest.Report{}, true, errors.New("run failed"))

	if notifier.calls != 0 {
		t.Fatalf("calls = %d, want 0", notifier.calls)
	}
}
