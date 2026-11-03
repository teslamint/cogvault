package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func openTest(t *testing.T) (*Archive, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "archive")
	a, err := Open(root)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { a.Close() })
	return a, root
}

func source(body, url string) SourceRequest {
	return SourceRequest{Content: strings.NewReader(body), OriginalName: "a.md", URL: url, Title: "A"}
}

func objectFiles(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "objects", "sha256"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func tempFiles(t *testing.T, root string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "objects", "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestCaptureStoresImmutableObjectSeparateFromMetadata(t *testing.T) {
	a, root := openTest(t)
	body := "complete source text\n"
	res, err := a.CaptureSource(context.Background(), source(body, "https://example.org/a"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(body))
	hexSum := hex.EncodeToString(sum[:])
	if res.ObjectID != "sha256:"+hexSum || res.Existing || !strings.HasPrefix(res.CaptureID, "cap_") {
		t.Fatalf("result = %+v", res)
	}
	objPath := filepath.Join(root, "objects", "sha256", hexSum)
	got, err := os.ReadFile(objPath)
	if err != nil || string(got) != body {
		t.Fatalf("object bytes = %q err=%v", got, err)
	}
	info, err := os.Stat(objPath)
	if err != nil || info.Mode().Perm()&0o222 != 0 {
		t.Fatalf("object must be read-only: mode=%v err=%v", info.Mode(), err)
	}
	c, err := a.Status(context.Background(), res.CaptureID)
	if err != nil {
		t.Fatal(err)
	}
	if c.Kind != "source" || c.URL != "https://example.org/a" || c.Size != int64(len(body)) ||
		c.Acquisition != "acquired" || c.Completeness != "unknown" {
		t.Fatalf("capture = %+v", c)
	}
	if len(tempFiles(t, root)) != 0 {
		t.Fatal("temporary files left behind")
	}
}

func TestIdenticalRequestIsIdempotentAndIDIsStable(t *testing.T) {
	a, root := openTest(t)
	first, err := a.CaptureSource(context.Background(), source("x", "https://example.org/a"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.CaptureSource(context.Background(), source("x", "https://example.org/a"))
	if err != nil {
		t.Fatal(err)
	}
	if second.CaptureID != first.CaptureID || !second.Existing {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
	list, err := a.List(context.Background())
	if err != nil || len(list) != 1 {
		t.Fatalf("list=%+v err=%v", list, err)
	}
	if len(objectFiles(t, root)) != 1 {
		t.Fatal("object stored more than once")
	}
	// A reopened registry derives the same ID for the same request.
	a.Close()
	b, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	third, err := b.CaptureSource(context.Background(), source("x", "https://example.org/a"))
	if err != nil || third.CaptureID != first.CaptureID || !third.Existing {
		t.Fatalf("third=%+v err=%v", third, err)
	}
}

func TestSameBytesWithDifferentProvenanceKeepBothRelations(t *testing.T) {
	a, root := openTest(t)
	one, err := a.CaptureSource(context.Background(), source("same", "https://a.example/x"))
	if err != nil {
		t.Fatal(err)
	}
	two, err := a.CaptureSource(context.Background(), source("same", "https://mirror.example/x"))
	if err != nil {
		t.Fatal(err)
	}
	if one.CaptureID == two.CaptureID || one.ObjectID != two.ObjectID || two.Existing {
		t.Fatalf("one=%+v two=%+v", one, two)
	}
	if n := len(objectFiles(t, root)); n != 1 {
		t.Fatalf("object files = %d, want 1", n)
	}
	c, err := a.Status(context.Background(), one.CaptureID)
	if err != nil {
		t.Fatal(err)
	}
	if c.URL != "https://a.example/x" || len(c.SameObject) != 1 || c.SameObject[0] != two.CaptureID {
		t.Fatalf("capture = %+v", c)
	}
}

func TestExplicitKeyReusedWithDifferentPayloadConflicts(t *testing.T) {
	a, root := openTest(t)
	req := source("v1", "https://example.org/a")
	req.Key = "report-7/source-1"
	first, err := a.CaptureSource(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	again := source("v1", "https://example.org/a")
	again.Key = "report-7/source-1"
	if res, err := a.CaptureSource(context.Background(), again); err != nil || res.CaptureID != first.CaptureID || !res.Existing {
		t.Fatalf("same key, same payload: res=%+v err=%v", res, err)
	}
	changed := source("v2", "https://example.org/a")
	changed.Key = "report-7/source-1"
	if _, err := a.CaptureSource(context.Background(), changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	list, _ := a.List(context.Background())
	if len(list) != 1 || len(objectFiles(t, root)) != 1 || len(tempFiles(t, root)) != 0 {
		t.Fatalf("conflict changed the archive: captures=%d objects=%v", len(list), objectFiles(t, root))
	}
}

func TestVerifiedCompletenessRequiresBasis(t *testing.T) {
	a, _ := openTest(t)
	req := source("x", "")
	req.Completeness = "verified"
	if _, err := a.CaptureSource(context.Background(), req); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	req = source("x", "")
	req.Completeness = "incomplete"
	res, err := a.CaptureSource(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := a.Status(context.Background(), res.CaptureID)
	if c.Acquisition != "partial" || c.Completeness != "incomplete" {
		t.Fatalf("capture = %+v", c)
	}
}

func TestRejectsInvalidURLAndOversizeWithoutStoring(t *testing.T) {
	a, root := openTest(t)
	if _, err := a.CaptureSource(context.Background(), source("x", "file:///etc/passwd")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
	big := SourceRequest{Content: bytes.NewReader(make([]byte, MaxObjectBytes+1)), OriginalName: "big.bin"}
	if _, err := a.CaptureSource(context.Background(), big); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if len(objectFiles(t, root)) != 0 || len(tempFiles(t, root)) != 0 {
		t.Fatal("rejected input left files behind")
	}
}

func TestTamperedObjectIsReportedNotOverwritten(t *testing.T) {
	a, root := openTest(t)
	res, err := a.CaptureSource(context.Background(), source("original", "https://a.example/x"))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(root, "objects", "sha256", strings.TrimPrefix(res.ObjectID, "sha256:"))
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, url := range []string{"https://b.example/x", "https://a.example/x"} {
		// The second URL repeats the original request exactly; an idempotent
		// retry must still verify the stored bytes.
		if res, err := a.CaptureSource(context.Background(), source("original", url)); !errors.Is(err, ErrIntegrity) {
			t.Fatalf("%s: res=%+v err=%v, want ErrIntegrity", url, res, err)
		}
	}
	if got, _ := os.ReadFile(p); string(got) != "tampered" {
		t.Fatal("existing object was overwritten")
	}
}

func TestOpenRefusesSymlinkedManagedPaths(t *testing.T) {
	for _, name := range []string{"objects", "registry.db", "registry.db-wal", "registry.db-shm", "registry.db-journal"} {
		t.Run(name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "archive")
			victim := filepath.Join(t.TempDir(), "victim")
			if err := os.WriteFile(victim, []byte("VICTIM"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(victim, filepath.Join(root, name)); err != nil {
				t.Fatal(err)
			}
			if a, err := Open(root); err == nil {
				a.Close()
				t.Fatalf("Open accepted a symlinked %s", name)
			}
			if got, _ := os.ReadFile(victim); string(got) != "VICTIM" {
				t.Fatalf("symlink target was modified: %q", got)
			}
		})
	}
	t.Run("root itself", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "rootlink")
		if err := os.Symlink(t.TempDir(), link); err != nil {
			t.Fatal(err)
		}
		if a, err := Open(link); err == nil {
			a.Close()
			t.Fatal("Open accepted a root that is a symlink")
		}
	})
	if _, err := Open("relative/root"); err == nil {
		t.Fatal("Open accepted a relative root")
	}
}

func TestReportRecordsOrderedCitationsWithoutClaimingOriginals(t *testing.T) {
	a, _ := openTest(t)
	src, err := a.CaptureSource(context.Background(), source("paper", "https://journal.example/p"))
	if err != nil {
		t.Fatal(err)
	}
	rep, err := a.ImportReport(context.Background(), ReportRequest{
		Content:      strings.NewReader("# Deep research report\n"),
		OriginalName: "report.md",
		Supplier:     "chatgpt-deep-research",
		Cites:        []string{"https://snippet.example/only", src.CaptureID},
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := a.Status(context.Background(), rep.CaptureID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != "report" || len(r.Citations) != 2 {
		t.Fatalf("report = %+v", r)
	}
	if c := r.Citations[0]; c.Position != 1 || c.URL != "https://snippet.example/only" || c.CaptureID != "" {
		t.Fatalf("uncaptured citation = %+v", c)
	}
	if c := r.Citations[1]; c.Position != 2 || c.CaptureID != src.CaptureID || c.Completeness != "unknown" {
		t.Fatalf("captured citation = %+v", c)
	}
	s, _ := a.Status(context.Background(), src.CaptureID)
	if len(s.CitedBy) != 1 || s.CitedBy[0] != rep.CaptureID {
		t.Fatalf("source cited_by = %+v", s.CitedBy)
	}
	list, _ := a.List(context.Background())
	if len(list) != 2 {
		t.Fatalf("a URL citation must not create a capture: %+v", list)
	}
}

func TestReportRejectsUnknownOrReportCitations(t *testing.T) {
	a, _ := openTest(t)
	if _, err := a.ImportReport(context.Background(), ReportRequest{Content: strings.NewReader("r"), Cites: []string{"cap_00000000000000000000000000000000"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	rep, err := a.ImportReport(context.Background(), ReportRequest{Content: strings.NewReader("r1")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ImportReport(context.Background(), ReportRequest{Content: strings.NewReader("r2"), Cites: []string{rep.CaptureID}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid for citing a report", err)
	}
}

func TestStatusUnknownCapture(t *testing.T) {
	a, _ := openTest(t)
	if _, err := a.Status(context.Background(), "cap_missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
