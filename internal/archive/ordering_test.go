package archive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestDeletionOrderIgnoresClockDirection: the clock moves backward between
// a restore and a later quarantine. Ordering by time would let the older
// restore win and bring the original back; ordering by seq must not.
func TestDeletionOrderIgnoresClockDirection(t *testing.T) {
	a, root, _ := handoffFixture(t)
	clk := withClock(a)
	res := mustCapture(t, a, source("clock skew\n", ""))
	quarantine(t, a, res.ObjectID)
	clk.t = clk.t.Add(time.Hour)
	if err := a.Unquarantine(ctx, res.ObjectID); err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(-48 * time.Hour) // clock jumps back
	quarantine(t, a, res.ObjectID)

	bdir := filepath.Join(t.TempDir(), "b")
	if _, err := a.Backup(ctx, bdir); err != nil {
		t.Fatal(err)
	}
	a.Close()
	b, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if _, err := b.Status(ctx, res.CaptureID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reopen after backward clock resurrected the capture: %v", err)
	}
	rep, err := RestoreTest(ctx, bdir, t.TempDir(), []string{filepath.Join(root, "deletions", deletionLogName)}, time.Now())
	if err != nil || !rep.Passed || len(rep.Resurrected) != 0 {
		t.Fatalf("restore test after backward clock = %+v, %v", rep, err)
	}
	restored, _, err := Restore(bdir, filepath.Join(t.TempDir(), "r"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if _, err := restored.Status(ctx, res.CaptureID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("restore after backward clock resurrected the capture: %v", err)
	}
}

func TestDeletionLogRejectsGapsAndDivergentMerges(t *testing.T) {
	a, root := openTest(t)
	res := mustCapture(t, a, source("log order\n", ""))
	quarantine(t, a, res.ObjectID)
	if err := a.Unquarantine(ctx, res.ObjectID); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(root, "deletions", deletionLogName)
	recs, err := ReadDeletionLog(live)
	if err != nil || len(recs) != 2 || recs[0].Seq != 1 || recs[1].Seq != 2 {
		t.Fatalf("log = %+v, %v", recs, err)
	}

	// A newer log extends an older prefix.
	if merged, err := mergeDeletionLogs(recs[:1], recs); err != nil || len(merged) != 2 {
		t.Fatalf("prefix merge = %+v, %v", merged, err)
	}
	// Same seq, different action: the logs diverged.
	other := append([]DeletionRecord(nil), recs...)
	other[1].Action = "purge"
	if _, err := mergeDeletionLogs(recs, other); !errors.Is(err, ErrConflict) {
		t.Fatalf("divergent merge: err = %v", err)
	}

	gap := filepath.Join(t.TempDir(), "gap.jsonl")
	rec := recs[1]
	if err := appendDeletion(gap, rec); err != nil { // seq 2 with no seq 1
		t.Fatal(err)
	}
	if _, err := ReadDeletionLog(gap); err == nil {
		t.Fatal("log starting at seq 2 accepted")
	}
}

// TestBackupIsConsistentUnderConcurrentWrites runs captures and deletions
// from a second Archive handle on the same root (as a second process
// would) while backups run. Every backup must verify, hold every object
// its registry snapshot references, and pass a restore test.
func TestBackupIsConsistentUnderConcurrentWrites(t *testing.T) {
	a, root := openTest(t)
	other, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	stop := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	var writeErrs []error
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			res, err := other.CaptureSource(ctx, source(fmt.Sprintf("concurrent %d\n", i), ""))
			if err == nil && i%3 == 0 {
				var im Impact
				if im, err = other.ImpactOf(ctx, res.ObjectID); err == nil {
					err = other.Quarantine(ctx, res.ObjectID, im.Digest)
				}
			}
			if err != nil {
				mu.Lock()
				writeErrs = append(writeErrs, err)
				mu.Unlock()
				return
			}
		}
	}()

	backups := t.TempDir()
	for i := range 8 {
		dir := filepath.Join(backups, fmt.Sprintf("b%d", i))
		if _, err := a.Backup(ctx, dir); err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("backup %d: %v", i, err)
		}
		if _, err := VerifyBackup(dir); err != nil {
			t.Fatalf("verify backup %d: %v", i, err)
		}
		if err := checkBackupObjects(ctx, dir); err != nil {
			t.Fatalf("backup %d inconsistent: %v", i, err)
		}
	}
	close(stop)
	wg.Wait()
	if len(writeErrs) > 0 {
		t.Fatalf("concurrent writer failed: %v", writeErrs)
	}
	rep, err := RestoreTest(ctx, filepath.Join(backups, "b7"), t.TempDir(), []string{filepath.Join(root, "deletions", deletionLogName)}, time.Now())
	if err != nil || !rep.Passed {
		t.Fatalf("restore test = %+v, %v", rep, err)
	}
}

// TestBackupFailsWhenAReferencedObjectIsMissing pins the consistency check
// itself: a registry that references an object the copy lacks is refused.
func TestBackupFailsWhenAReferencedObjectIsMissing(t *testing.T) {
	a, root := openTest(t)
	res := mustCapture(t, a, source("missing from backup\n", ""))
	obj := filepath.Join(root, "objects", "sha256", hexOf(res.ObjectID))
	os.Chmod(obj, 0o644)
	if err := os.Remove(obj); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "b")
	if _, err := a.Backup(ctx, dir); !errors.Is(err, ErrIntegrity) || !strings.Contains(err.Error(), "inconsistent") {
		t.Fatalf("backup with a missing object: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, backupManifestName)); !os.IsNotExist(err) {
		t.Fatal("an inconsistent backup got a manifest")
	}
}
