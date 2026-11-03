package archive

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestMoveIfPresentKeepsTheVerifiedCopy(t *testing.T) {
	good := []byte("object bytes\n")
	sum := sha256.Sum256(good)
	hexSum, size := hex.EncodeToString(sum[:]), int64(len(good))
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	write := func(p string, b []byte) {
		t.Helper()
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			return "<missing>"
		}
		return string(b)
	}

	// A damaged dst is replaced by a verified src.
	write(src, good)
	write(dst, []byte("damaged"))
	if err := moveIfPresent(src, dst, hexSum, size); err != nil || read(dst) != string(good) || read(src) != "<missing>" {
		t.Fatalf("damaged dst: err=%v dst=%q src=%q", err, read(dst), read(src))
	}
	// A verified dst is kept and the stale src removed.
	write(src, []byte("stale"))
	if err := moveIfPresent(src, dst, hexSum, size); err != nil || read(dst) != string(good) || read(src) != "<missing>" {
		t.Fatalf("verified dst: err=%v dst=%q src=%q", err, read(dst), read(src))
	}
	// Neither matches: nothing is removed.
	write(src, []byte("bad one"))
	write(dst, []byte("bad two"))
	if err := moveIfPresent(src, dst, hexSum, size); !errors.Is(err, ErrIntegrity) || read(src) != "bad one" || read(dst) != "bad two" {
		t.Fatalf("both damaged: err=%v dst=%q src=%q", err, read(dst), read(src))
	}
}

func TestUnquarantineRefusesMissingOrDamagedBytes(t *testing.T) {
	a, root := openTest(t)
	res := mustCapture(t, a, source("restore me\n", ""))
	quarantine(t, a, res.ObjectID)
	q := filepath.Join(root, "quarantine", hexOf(res.ObjectID))
	body, err := os.ReadFile(q)
	if err != nil {
		t.Fatal(err)
	}
	stateOf := func() string {
		var s string
		a.db.QueryRow(`SELECT state FROM objects WHERE object_id = ?`, res.ObjectID).Scan(&s)
		return s
	}
	os.Chmod(q, 0o600)
	if err := os.WriteFile(q, []byte("damaged!!!\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.Unquarantine(ctx, res.ObjectID); !errors.Is(err, ErrIntegrity) || stateOf() != "quarantined" {
		t.Fatalf("damaged quarantined file: err=%v state=%s", err, stateOf())
	}
	if err := os.Remove(q); err != nil {
		t.Fatal(err)
	}
	if err := a.Unquarantine(ctx, res.ObjectID); !errors.Is(err, ErrIntegrity) || stateOf() != "quarantined" {
		t.Fatalf("missing quarantined file: err=%v state=%s", err, stateOf())
	}
	if recs, _ := ReadDeletionLog(filepath.Join(root, "deletions", deletionLogName)); len(recs) != 1 {
		t.Fatalf("a refused restore was logged: %+v", recs)
	}
	if err := os.WriteFile(q, body, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := a.Unquarantine(ctx, res.ObjectID); err != nil || stateOf() != "active" {
		t.Fatalf("intact file: err=%v state=%s", err, stateOf())
	}
}

func TestOversizedDeletionRecordIsRefusedBeforeWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), deletionLogName)
	ok := DeletionRecord{Version: 1, Seq: 1, Action: "quarantine", ObjectID: "sha256:" + strings.Repeat("a", 64), CaptureIDs: []string{}, At: "2026-01-01T00:00:00Z"}
	if err := appendDeletion(path, ok); err != nil {
		t.Fatal(err)
	}
	big := ok
	big.Seq = 2
	for i := 0; len(big.CaptureIDs)*30 < maxDeletionRecord; i++ {
		big.CaptureIDs = append(big.CaptureIDs, fmt.Sprintf("cap_%032x", i))
	}
	if err := appendDeletion(path, big); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized record: err = %v", err)
	}
	if recs, err := ReadDeletionLog(path); err != nil || len(recs) != 1 {
		t.Fatalf("log after refusal = %d records, %v", len(recs), err)
	}
}

func TestOpenRegularRefusesAFIFOWithoutBlocking(t *testing.T) {
	p := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(p, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		f, err := openRegular(p)
		if f != nil {
			f.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrIntegrity) {
			t.Fatalf("FIFO: err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("opening a FIFO blocked")
	}
	if err := verifyFile(p, strings.Repeat("0", 64), 0); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("verifyFile on a FIFO: %v", err)
	}
}

func TestCaptureMapUpdatesAreSerializedAndBounded(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			obj := fmt.Sprintf("%064x", i)
			errs <- updateCaptureMap(dir, []PickupResult{{CaptureID: fmt.Sprintf("cap_%032x", i), ObjectID: "sha256:" + obj, SourcePath: filepath.Join(dir, obj+".md"), Status: "copied"}})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	m, err := readCaptureMap(dir)
	if err != nil || len(m.Entries) != 16 {
		t.Fatalf("map after 16 overlapping updates = %d entries, %v", len(m.Entries), err)
	}

	// A map that would pass the size limit is refused and the old map stays.
	big := captureMap{Version: 1, Entries: map[string]CaptureMapEntry{}}
	ids := make([]string, 0, maxCaptureMapSize/48)
	for i := range cap(ids) {
		ids = append(ids, fmt.Sprintf("cap_%032x", i))
	}
	big.Entries["big.md"] = CaptureMapEntry{ObjectID: "sha256:" + strings.Repeat("b", 64), CaptureIDs: ids}
	body, _ := json.Marshal(big)
	if len(body) > maxCaptureMapSize {
		t.Fatalf("fixture too large: %d", len(body))
	}
	mapPath := filepath.Join(dir, CaptureMapName)
	if err := os.WriteFile(mapPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	obj := strings.Repeat("c", 64)
	err = updateCaptureMap(dir, []PickupResult{{CaptureID: "cap_" + strings.Repeat("c", 32), ObjectID: "sha256:" + obj, SourcePath: filepath.Join(dir, obj+".md"), Status: "copied"}})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized map: err = %v", err)
	}
	if after, _ := os.ReadFile(mapPath); string(after) != string(body) {
		t.Fatal("a refused update changed the map")
	}
}

func TestBackupKeepsObjectsReadOnly(t *testing.T) {
	a, _ := openTest(t)
	res := mustCapture(t, a, source("read only\n", ""))
	bdir := filepath.Join(t.TempDir(), "b")
	if _, err := a.Backup(ctx, bdir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(bdir, "objects", "sha256", hexOf(res.ObjectID)))
	if err != nil || info.Mode().Perm() != 0o444 {
		t.Fatalf("backed-up object mode = %v, %v", info.Mode().Perm(), err)
	}
}
