package archive

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func withClock(a *Archive) *clock {
	c := &clock{t: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)}
	a.now = c.now
	return c
}

func hexOf(objectID string) string { return strings.TrimPrefix(objectID, "sha256:") }

func TestImpactListsDependentsAndQuarantineNeedsCurrentDigest(t *testing.T) {
	a, root, dest := handoffFixture(t)
	src := mustCapture(t, a, verified("delete me\n", "d.md", "https://example.org/d"))
	twin := mustCapture(t, a, verified("delete me\n", "twin.md", "https://mirror.example.org/d"))
	rep, err := a.ImportReport(ctx, ReportRequest{Content: strings.NewReader("rep"), OriginalName: "r.md", Cites: []string{src.CaptureID}})
	if err != nil {
		t.Fatal(err)
	}
	sum, err := a.CaptureSummary(ctx, SummaryRequest{Content: strings.NewReader("sum"), OriginalName: "s.md", SummaryOf: src.CaptureID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Handoff(ctx, DefaultHandoffTypes); err != nil {
		t.Fatal(err)
	}
	if _, err := Pickup(root, dest, DefaultHandoffTypes); err != nil {
		t.Fatal(err)
	}
	lookup := func(p, h string) (LedgerEntry, bool, error) {
		return LedgerEntry{Status: "success", WikiPage: "sources/d.md"}, true, nil
	}
	if _, err := WriteReceipts(root, dest, lookup, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CollectReceipts(ctx); err != nil {
		t.Fatal(err)
	}

	im, err := a.ImpactOf(ctx, src.ObjectID)
	if err != nil {
		t.Fatal(err)
	}
	if im.State != "active" || len(im.Captures) != 2 || len(im.Reports) != 1 || im.Reports[0] != rep.CaptureID ||
		len(im.Summaries) != 1 || im.Summaries[0] != sum.CaptureID || len(im.Manifests) != 2 ||
		len(im.IngestCopies) != 1 || len(im.SourcePages) != 1 || im.SourcePages[0] != "sources/d.md" || im.Digest == "" {
		t.Fatalf("impact = %+v", im)
	}
	if obj, err := a.ResolveObject(ctx, twin.CaptureID); err != nil || obj != src.ObjectID {
		t.Fatalf("ResolveObject(capture) = %s, %v", obj, err)
	}

	// The impact changes when a new capture of the bytes arrives; the old
	// digest is then stale.
	mustCapture(t, a, source("delete me\n", "https://third.example.org/d"))
	if err := a.Quarantine(ctx, src.ObjectID, im.Digest); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale digest: err = %v, want ErrConflict", err)
	}
	if im2, _ := a.ImpactOf(ctx, src.ObjectID); im2.Digest == im.Digest {
		t.Fatal("digest did not change with the impact")
	}
}

func quarantine(t *testing.T, a *Archive, objectID string) {
	t.Helper()
	im, err := a.ImpactOf(ctx, objectID)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Quarantine(ctx, objectID, im.Digest); err != nil {
		t.Fatal(err)
	}
}

func TestQuarantineHidesRestoresAndBlocksRecapture(t *testing.T) {
	a, root, _ := handoffFixture(t)
	res := mustCapture(t, a, verified("quarantined bytes\n", "q.md", "https://example.org/q"))
	if _, err := a.Handoff(ctx, DefaultHandoffTypes); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(root, "outbox", res.CaptureID+".json")
	quarantine(t, a, res.ObjectID)

	if _, err := a.Status(ctx, res.CaptureID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("status of a quarantined capture: err = %v", err)
	}
	if list, _ := a.List(ctx); len(list) != 0 {
		t.Fatalf("list shows quarantined captures: %+v", list)
	}
	if _, err := os.Stat(manifest); !os.IsNotExist(err) {
		t.Fatal("quarantine left the hand-off manifest")
	}
	if out, _ := a.Handoff(ctx, DefaultHandoffTypes); len(out) != 0 {
		t.Fatalf("handoff after quarantine = %+v", out)
	}
	if _, err := os.Stat(filepath.Join(root, "objects", "sha256", hexOf(res.ObjectID))); !os.IsNotExist(err) {
		t.Fatal("object still under objects/")
	}
	if _, err := os.Stat(filepath.Join(root, "quarantine", hexOf(res.ObjectID))); err != nil {
		t.Fatalf("object not in quarantine: %v", err)
	}
	for _, req := range []SourceRequest{
		verified("quarantined bytes\n", "q.md", "https://example.org/q"),
		source("quarantined bytes\n", "https://other.example.org/q"),
	} {
		if _, err := a.CaptureSource(ctx, req); !errors.Is(err, ErrDeleted) {
			t.Fatalf("recapture of quarantined bytes: err = %v, want ErrDeleted", err)
		}
	}
	if _, err := a.CaptureSummary(ctx, SummaryRequest{Content: strings.NewReader("s"), OriginalName: "s.md", SummaryOf: res.CaptureID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("summary of a quarantined capture: err = %v", err)
	}

	log, err := os.ReadFile(filepath.Join(root, "deletions", deletionLogName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(log), "quarantined bytes") || strings.Contains(string(log), "example.org") {
		t.Fatalf("deletion log holds content or provenance: %s", log)
	}

	if err := a.Unquarantine(ctx, res.ObjectID); err != nil {
		t.Fatal(err)
	}
	if c := mustStatus(t, a, res.CaptureID); c.ObjectID != res.ObjectID {
		t.Fatalf("restored status = %+v", c)
	}
	if again, err := a.CaptureSource(ctx, verified("quarantined bytes\n", "q.md", "https://example.org/q")); err != nil || !again.Existing {
		t.Fatalf("identical retry after restore = %+v, %v", again, err)
	}
}

func TestPurgeNeedsConfirmationAndQuarantineMinimum(t *testing.T) {
	a, root, _ := handoffFixture(t)
	clk := withClock(a)
	res := mustCapture(t, a, source("purge me\n", "https://example.org/p"))
	if err := a.Purge(ctx, res.ObjectID, res.ObjectID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("purge of an active object: err = %v", err)
	}
	quarantine(t, a, res.ObjectID)
	clk.t = clk.t.Add(QuarantineMinimum - time.Second)
	if err := a.Purge(ctx, res.ObjectID, res.ObjectID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("purge before the quarantine minimum: err = %v", err)
	}
	clk.t = clk.t.Add(time.Second)
	if err := a.Purge(ctx, res.ObjectID, res.CaptureID); !errors.Is(err, ErrInvalid) {
		t.Fatalf("purge with a wrong confirmation: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "quarantine", hexOf(res.ObjectID))); err != nil {
		t.Fatal("eligible quarantine expired without a purge")
	}
	if err := a.Purge(ctx, res.ObjectID, res.ObjectID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "quarantine", hexOf(res.ObjectID))); !os.IsNotExist(err) {
		t.Fatal("purged object still on disk")
	}
	if im, _ := a.ImpactOf(ctx, res.ObjectID); im.State != "purged" {
		t.Fatalf("state after purge = %s", im.State)
	}
	if err := a.Unquarantine(ctx, res.ObjectID); err == nil {
		t.Fatal("a purged object was restored")
	}
	if _, err := a.CaptureSource(ctx, source("purge me\n", "https://example.org/p")); !errors.Is(err, ErrDeleted) {
		t.Fatalf("recapture of purged bytes: err = %v", err)
	}
}

func TestOpenReappliesDeletionLogAfterCrash(t *testing.T) {
	a, root := openTest(t)
	res := mustCapture(t, a, source("crash between log and apply\n", ""))
	a.Close()
	// The record reached the log, but the process stopped before applying it.
	if err := appendDeletion(filepath.Join(root, "deletions", deletionLogName), DeletionRecord{Version: 1, Seq: 1, Action: "quarantine", ObjectID: res.ObjectID, CaptureIDs: []string{res.CaptureID}, At: time.Now().UTC().Format(time.RFC3339Nano)}); err != nil {
		t.Fatal(err)
	}
	b, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err := b.Status(ctx, res.CaptureID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("status after re-applied quarantine: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "quarantine", hexOf(res.ObjectID))); err != nil {
		t.Fatalf("object not moved on Open: %v", err)
	}

	os.WriteFile(filepath.Join(root, "deletions", deletionLogName), []byte("{\"version\":1}\n"), 0o600)
	b.Close()
	if _, err := Open(root); err == nil {
		t.Fatal("Open accepted a damaged deletion log")
	}
}

func TestBackupRestoreReappliesNewerDeletions(t *testing.T) {
	a, root, _ := handoffFixture(t)
	clk := withClock(a)
	keep := mustCapture(t, a, verified("keep me\n", "k.md", ""))
	gone := mustCapture(t, a, source("quarantine after backup\n", ""))
	purged := mustCapture(t, a, source("purge after backup\n", ""))
	quarantine(t, a, purged.ObjectID) // quarantined before the backup, purged after

	backups := t.TempDir()
	bdir := filepath.Join(backups, "b1")
	m, err := a.Backup(ctx, bdir)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Files) == 0 {
		t.Fatal("empty backup manifest")
	}
	if _, err := a.Backup(ctx, bdir); err == nil {
		t.Fatal("backup overwrote an existing directory")
	}

	quarantine(t, a, gone.ObjectID)
	clk.t = clk.t.Add(QuarantineMinimum)
	if err := a.Purge(ctx, purged.ObjectID, purged.ObjectID); err != nil {
		t.Fatal(err)
	}
	liveLog := filepath.Join(root, "deletions", deletionLogName)

	// Restoring the backup alone brings the later deletions back: the live
	// log is required.
	stale, _, err := Restore(bdir, filepath.Join(t.TempDir(), "stale"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stale.Status(ctx, gone.CaptureID); err != nil {
		t.Fatalf("fixture check: the backup alone should still show the later-deleted capture: %v", err)
	}
	stale.Close()

	rep, err := RestoreTest(ctx, bdir, t.TempDir(), []string{liveLog}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Passed || len(rep.Resurrected) != 0 || rep.ObjectsVerified != 1 || rep.DeletionsReapplied != 2 {
		t.Fatalf("restore test = %+v", rep)
	}

	restored, _, err := Restore(bdir, filepath.Join(t.TempDir(), "restored"), []string{liveLog})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if c := mustStatus(t, restored, keep.CaptureID); c.Completeness != "verified" {
		t.Fatalf("kept capture after restore = %+v", c)
	}
	for _, id := range []string{gone.CaptureID, purged.CaptureID} {
		if _, err := restored.Status(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("deleted capture %s came back after restore: %v", id, err)
		}
	}
	if _, err := os.Stat(filepath.Join(restored.root, "quarantine", hexOf(purged.ObjectID))); !os.IsNotExist(err) {
		t.Fatal("purged object came back in quarantine")
	}
	if _, err := os.Stat(filepath.Join(restored.root, "objects", "sha256", hexOf(purged.ObjectID))); !os.IsNotExist(err) {
		t.Fatal("purged object came back as active")
	}
}

func TestRestoreTestFailsOnTamperedBackup(t *testing.T) {
	a, _ := openTest(t)
	res := mustCapture(t, a, source("backup body\n", ""))
	bdir := filepath.Join(t.TempDir(), "b")
	if _, err := a.Backup(ctx, bdir); err != nil {
		t.Fatal(err)
	}
	obj := filepath.Join(bdir, "objects", "sha256", hexOf(res.ObjectID))
	os.Chmod(obj, 0o644)
	os.WriteFile(obj, []byte("tampered\n"), 0o644)
	if _, err := VerifyBackup(bdir); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("VerifyBackup: err = %v", err)
	}
	rep, err := RestoreTest(ctx, bdir, t.TempDir(), nil, time.Now())
	if err != nil || rep.Passed || len(rep.Errors) == 0 {
		t.Fatalf("restore test of a tampered backup = %+v, %v", rep, err)
	}
	report := filepath.Join(t.TempDir(), "report.json")
	os.WriteFile(report, []byte(`{"version":1,"passed":false,"backup":"","backup_created_at":"","checked_at":"","objects_verified":0,"deletions_reapplied":0,"resurrected":[],"errors":["x"]}`), 0o600)
	if _, err := ReadRestoreReport(report); err == nil {
		t.Fatal("a failed report was accepted")
	}
}

// Status takes the archive lock exclusively, so no write (shared) or
// deletion action (exclusive) lands between its queries: a capture added
// while a writer holds the lock is either wholly in a later status or the
// status ran before the writer took the lock.
func TestStatusIsOnePointInTimeAgainstAConcurrentWrite(t *testing.T) {
	a, _ := openTest(t)
	res := mustCapture(t, a, verified("status bytes\n", "s.md", "https://example.org/s"))
	unlock, err := a.lock(false) // a writer in the middle of its operation
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		c   Capture
		err error
	}
	done := make(chan result, 1)
	go func() {
		c, err := a.Status(ctx, res.CaptureID)
		done <- result{c, err}
	}()
	select {
	case r := <-done:
		unlock()
		t.Fatalf("Status ran while a writer held the archive lock: %+v, %v", r.c.SameObject, r.err)
	case <-time.After(200 * time.Millisecond):
	}
	// The writer adds a relation (a second capture of the same bytes);
	// Capture's own shared lock is compatible with the one held above.
	two := mustCapture(t, a, verified("status bytes\n", "s.md", "https://example.org/s2"))
	unlock()
	r := <-done
	if r.err != nil {
		t.Fatalf("Status after the writer finished: %v", r.err)
	}
	if len(r.c.SameObject) != 1 || r.c.SameObject[0] != two.CaptureID {
		t.Fatalf("Status did not see the whole write: same_object = %v, want [%s]", r.c.SameObject, two.CaptureID)
	}
}
