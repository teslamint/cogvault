package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/teslamint/cogvault/internal/archive"
)

// The queue is shared state, not process state: a job submitted by one
// command is read by another, the worker records why a loopback target was
// refused (no connection is made), and a retry puts the job back with its
// history intact.
func TestArchiveJobSubmitRunStatusRetry(t *testing.T) {
	root := filepath.Join(t.TempDir(), "archive")
	run := func(args ...string) (string, string) {
		t.Helper()
		out, errOut, err := executeCommand(append([]string{"archive", "--root", root}, args...)...)
		if err != nil {
			t.Fatalf("archive %s: %v\nstdout: %s\nstderr: %s", strings.Join(args, " "), err, out, errOut)
		}
		return out, errOut
	}

	out, _ := run("job", "submit", "http://127.0.0.1:9/secret", "--submitter", "chat-a", "--claim", "p. 3", "--json")
	var job archive.Job
	if err := json.Unmarshal([]byte(out), &job); err != nil {
		t.Fatalf("submit output is not a job: %v\n%s", err, out)
	}
	if job.State != "queued" || job.Submitter != "chat-a" || job.ClaimRef != "p. 3" {
		t.Fatalf("submitted job = %+v", job)
	}

	_, errOut := run("job", "submit", "http://127.0.0.1:9/secret", "--submitter", "chat-b", "--claim", "p. 3")
	if !strings.Contains(errOut, "existing job returned") {
		t.Fatalf("second client did not get the existing job: %q", errOut)
	}

	out, _ = run("job", "run", "--once", "--owner", "w-test")
	if !strings.Contains(out, job.JobID+": failed") {
		t.Fatalf("run output = %q", out)
	}
	out, _ = run("job", "status", job.JobID)
	if !strings.Contains(out, "failed") || !strings.Contains(out, "not a public address") || !strings.Contains(out, "attempt 1 by w-test") {
		t.Fatalf("status = %q", out)
	}

	out, _ = run("job", "retry", job.JobID, "--json")
	var again archive.Job
	if err := json.Unmarshal([]byte(out), &again); err != nil {
		t.Fatal(err)
	}
	if again.State != "queued" || len(again.Attempts) != 1 || again.MaxAttempts != 6 {
		t.Fatalf("after retry = %+v", again)
	}

	if _, _, err := executeCommand("archive", "--root", root, "job", "submit", "http://example.org/a?token=1", "--submitter", "x"); err == nil {
		t.Fatal("a URL with a query was queued")
	}
	if _, _, err := executeCommand("archive", "--root", root, "job", "status", "job_missing"); err == nil {
		t.Fatal("status of a missing job succeeded")
	}
}
