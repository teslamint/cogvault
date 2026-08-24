package main

import (
	"errors"
	"testing"

	"github.com/teslamint/cogvault/internal/ingest"
)

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
