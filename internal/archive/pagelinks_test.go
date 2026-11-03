package archive

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// linkFixture hands off and picks up every verified capture, then returns
// a function that writes receipts from the ledger and collects them.
func linkFixture(t *testing.T) (a *Archive, root, dest string, ledger fakeLedger, sync func(time.Time) int) {
	t.Helper()
	a, root, dest = handoffFixture(t)
	ledger = fakeLedger{}
	sync = func(now time.Time) int {
		t.Helper()
		if _, err := a.Handoff(ctx, DefaultHandoffTypes); err != nil {
			t.Fatal(err)
		}
		if _, err := Pickup(root, dest, DefaultHandoffTypes); err != nil {
			t.Fatal(err)
		}
		if _, err := WriteReceipts(root, dest, ledger.lookup, now); err != nil {
			t.Fatal(err)
		}
		n, err := a.CollectReceipts(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	return a, root, dest, ledger, sync
}

func srcOf(dest string, r Result) string {
	return SourcePath(dest, Manifest{ObjectID: r.ObjectID, Extension: "md"})
}

func linkPairs(t *testing.T, a *Archive, page string) map[string]PageLink {
	t.Helper()
	links, err := a.PageLinksFor(ctx, page)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]PageLink{}
	for _, l := range links {
		if _, dup := out[l.CaptureID]; dup {
			t.Fatalf("capture %s linked twice to %s", l.CaptureID, page)
		}
		out[l.CaptureID] = l
	}
	return out
}

func TestLaterSameBytesCaptureIsLinkedWithItsOwnProvenance(t *testing.T) {
	a, _, dest, ledger, sync := linkFixture(t)
	t0 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	first := mustCapture(t, a, verified("same bytes\n", "p.md", "https://example.org/a"))
	ledger[srcOf(dest, first)+"|"+hexOf(first.ObjectID)] = LedgerEntry{Status: "success", WikiPage: "sources/p.md", DigestedAt: "2026-04-01T00:00:00Z"}
	if n := sync(t0); n != 1 {
		t.Fatalf("collected %d", n)
	}
	before := linkPairs(t, a, "sources/p.md")[first.CaptureID]
	if before.BasisReceipt != first.CaptureID+".1.json" || before.ContentHash != hexOf(first.ObjectID) || !before.Current {
		t.Fatalf("first link = %+v", before)
	}

	// A mirror capture of the same bytes reuses the source path and ledger
	// row; it gets its own receipt and link, and the first link is kept.
	mirror := mustCapture(t, a, verified("same bytes\n", "p.md", "https://mirror.example.org/a"))
	if mirror.ObjectID != first.ObjectID || mirror.CaptureID == first.CaptureID {
		t.Fatalf("fixture: mirror = %+v", mirror)
	}
	if n := sync(t0.Add(time.Hour)); n != 1 {
		t.Fatalf("collected %d for the mirror", n)
	}
	links := linkPairs(t, a, "sources/p.md")
	if len(links) != 2 || links[first.CaptureID] != before {
		t.Fatalf("links = %+v; the first link must stay unchanged", links)
	}
	if l := links[mirror.CaptureID]; l.BasisReceipt != mirror.CaptureID+".1.json" || l.ObjectID != first.ObjectID || !l.Current {
		t.Fatalf("mirror link = %+v", l)
	}
	if s := mustStatus(t, a, mirror.CaptureID); len(s.PageLinks) != 1 || s.PageLinks[0].WikiPage != "sources/p.md" {
		t.Fatalf("mirror status links = %+v", s.PageLinks)
	}

	// Retry and restart: re-running and reopening add nothing.
	if n := sync(t0.Add(2 * time.Hour)); n != 0 {
		t.Fatalf("re-run collected %d", n)
	}
	reopened, err := Open(a.root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if again := linkPairs(t, reopened, "sources/p.md"); len(again) != 2 || again[first.CaptureID] != before {
		t.Fatalf("after reopen = %+v", again)
	}
}

func TestLinksKeepVersionsApartAndTrackLaterObservations(t *testing.T) {
	a, _, dest, ledger, sync := linkFixture(t)
	t0 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	v1 := mustCapture(t, a, verified("edition one\n", "e.md", "https://example.org/e"))
	v2 := mustCapture(t, a, verified("edition two\n", "e.md", "https://example.org/e2"))
	k1 := srcOf(dest, v1) + "|" + hexOf(v1.ObjectID)
	k2 := srcOf(dest, v2) + "|" + hexOf(v2.ObjectID)
	ledger[k1] = LedgerEntry{Status: "success", WikiPage: "sources/e.md"}
	ledger[k2] = LedgerEntry{Status: "success", WikiPage: "sources/e-2.md"}
	sync(t0)
	if l := linkPairs(t, a, "sources/e.md"); len(l) != 1 || l[v1.CaptureID].ContentHash != hexOf(v1.ObjectID) {
		t.Fatalf("other bytes leaked into sources/e.md: %+v", l)
	}
	if l := linkPairs(t, a, "sources/e-2.md"); len(l) != 1 || l[v2.CaptureID].ObjectID != v2.ObjectID {
		t.Fatalf("sources/e-2.md = %+v", l)
	}

	// The ledger later reports a failure: the link stays recorded but is no
	// longer current. A re-digest to another page adds a second link.
	ledger[k1] = LedgerEntry{Status: "failed", WikiPage: "sources/e.md"}
	sync(t0.Add(time.Hour))
	if l := linkPairs(t, a, "sources/e.md")[v1.CaptureID]; l.Current {
		t.Fatalf("link after a failed observation = %+v", l)
	}
	ledger[k1] = LedgerEntry{Status: "success", WikiPage: "sources/moved/e.md"}
	sync(t0.Add(2 * time.Hour))
	s := mustStatus(t, a, v1.CaptureID)
	if len(s.PageLinks) != 2 || s.PageLinks[0].WikiPage != "sources/e.md" || s.PageLinks[0].Current || s.PageLinks[1].WikiPage != "sources/moved/e.md" || !s.PageLinks[1].Current {
		t.Fatalf("v1 links = %+v", s.PageLinks)
	}
	if s.PageLinks[0].BasisReceipt != v1.CaptureID+".1.json" || s.PageLinks[1].BasisReceipt != v1.CaptureID+".3.json" {
		t.Fatalf("basis receipts = %+v", s.PageLinks)
	}

	// The ledger returns to the first page with a new digest time: the first
	// link becomes current again and keeps its first basis receipt.
	ledger[k1] = LedgerEntry{Status: "success", WikiPage: "sources/e.md", DigestedAt: "2026-04-01T03:00:00Z"}
	sync(t0.Add(3 * time.Hour))
	l := linkPairs(t, a, "sources/e.md")[v1.CaptureID]
	if !l.Current || l.BasisReceipt != v1.CaptureID+".1.json" || l.DigestedAt != "" {
		t.Fatalf("relinked = %+v; the first basis must stay", l)
	}
}

func TestForgedReceiptsAndPagesLinkNothing(t *testing.T) {
	a, root, dest, ledger, sync := linkFixture(t)
	real := mustCapture(t, a, verified("real\n", "r.md", "https://example.org/r"))
	held := mustCapture(t, a, source("never handed off\n", ""))
	ledger[srcOf(dest, real)+"|"+hexOf(real.ObjectID)] = LedgerEntry{Status: "success", WikiPage: "sources/r.md"}
	failed := mustCapture(t, a, verified("failed digest\n", "f.md", "https://example.org/f"))
	ledger[srcOf(dest, failed)+"|"+hexOf(failed.ObjectID)] = LedgerEntry{Status: "failed", WikiPage: "sources/f.md"}
	sync(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	if s := mustStatus(t, a, failed.CaptureID); len(s.PageLinks) != 0 || s.Ingest.LedgerStatus != "failed" {
		t.Fatalf("a failed observation linked: %+v", s.PageLinks)
	}

	forged := `{"version":1,"capture_id":"` + held.CaptureID + `","object_id":"` + held.ObjectID + `","source_path":"/x","content_hash":"` + hexOf(held.ObjectID) + `","ledger_status":"success","wiki_page":"sources/r.md","digested_at":"","observed_at":"2026-04-02T00:00:00Z"}`
	name := filepath.Join(root, "receipts", held.CaptureID+".1.json")
	if err := os.WriteFile(name, []byte(forged), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CollectReceipts(ctx); !errors.Is(err, ErrInvalid) {
		t.Fatalf("forged receipt: err = %v", err)
	}
	os.Remove(name)
	if l := linkPairs(t, a, "sources/r.md"); len(l) != 1 || l[real.CaptureID].CaptureID == "" {
		t.Fatalf("links after a forged receipt = %+v", l)
	}
	for _, bad := range []string{"", "../r.md", "/abs/r.md", "sources/r.txt", `sources\r.md`, "sources/./r.md"} {
		if _, err := a.PageLinksFor(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("PageLinksFor(%q): err = %v", bad, err)
		}
	}
	if l := linkPairs(t, a, "sources/never.md"); len(l) != 0 {
		t.Fatalf("unknown page = %+v", l)
	}

	// A receipt whose object differs from its capture is refused at link
	// time too (defense behind checkReceipt).
	r := Receipt{Version: receiptVersion, CaptureID: real.CaptureID, ObjectID: held.ObjectID, ContentHash: hexOf(held.ObjectID), LedgerStatus: "success", WikiPage: "sources/r.md"}
	if err := linkReceipt(ctx, a.db, "x.json", r, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("object mismatch: err = %v", err)
	}
	r.ContentHash = hexOf(real.ObjectID)
	if err := linkReceipt(ctx, a.db, "x.json", r, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("object mismatch with a matching hash: err = %v", err)
	}
	r.CaptureID = "cap_unknown"
	if err := linkReceipt(ctx, a.db, "x.json", r, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown capture: err = %v", err)
	}
}

func TestQuarantinedCaptureLinksAreHidden(t *testing.T) {
	a, _, dest, ledger, sync := linkFixture(t)
	res := mustCapture(t, a, verified("to quarantine\n", "q.md", "https://example.org/q"))
	ledger[srcOf(dest, res)+"|"+hexOf(res.ObjectID)] = LedgerEntry{Status: "success", WikiPage: "sources/q.md"}
	sync(time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	quarantine(t, a, res.ObjectID)
	if l := linkPairs(t, a, "sources/q.md"); len(l) != 0 {
		t.Fatalf("quarantined links = %+v", l)
	}
	if err := a.Unquarantine(ctx, res.ObjectID); err != nil {
		t.Fatal(err)
	}
	if l := linkPairs(t, a, "sources/q.md"); len(l) != 1 {
		t.Fatalf("links after unquarantine = %+v", l)
	}
}

func TestLinksSurviveBackupRestoreAndV2Upgrade(t *testing.T) {
	a, root, dest, ledger, sync := linkFixture(t)
	t0 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	first := mustCapture(t, a, verified("backed up\n", "b.md", "https://example.org/b"))
	key := srcOf(dest, first) + "|" + hexOf(first.ObjectID)
	ledger[key] = LedgerEntry{Status: "success", WikiPage: "sources/b.md"}
	sync(t0)
	want := linkPairs(t, a, "sources/b.md")

	// A second capture's receipt is written but not collected before the
	// backup; collecting it on the restored archive links it there.
	mirror := mustCapture(t, a, verified("backed up\n", "b.md", "https://mirror.example.org/b"))
	if _, err := a.Handoff(ctx, DefaultHandoffTypes); err != nil {
		t.Fatal(err)
	}
	if _, err := Pickup(root, dest, DefaultHandoffTypes); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteReceipts(root, dest, ledger.lookup, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	bdir := filepath.Join(t.TempDir(), "b")
	if _, err := a.Backup(ctx, bdir); err != nil {
		t.Fatal(err)
	}
	restored, _, err := Restore(bdir, filepath.Join(t.TempDir(), "restored"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if got := linkPairs(t, restored, "sources/b.md"); len(got) != 1 || got[first.CaptureID] != want[first.CaptureID] {
		t.Fatalf("restored links = %+v, want %+v", got, want)
	}
	if n, err := restored.CollectReceipts(ctx); err != nil || n != 1 {
		t.Fatalf("collect on restored = %d, %v", n, err)
	}
	if got := linkPairs(t, restored, "sources/b.md"); len(got) != 2 || got[mirror.CaptureID].BasisReceipt != mirror.CaptureID+".1.json" {
		t.Fatalf("restored links after collect = %+v", got)
	}

	// A version 2 registry (no page_links) is upgraded on Open and links
	// every receipt it already collected, with the same basis.
	if _, err := a.CollectReceipts(ctx); err != nil {
		t.Fatal(err)
	}
	want = linkPairs(t, a, "sources/b.md")
	if _, err := a.db.Exec(`DROP TABLE page_links; PRAGMA user_version = 2`); err != nil {
		t.Fatal(err)
	}
	a.Close()
	up, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	var v int
	if err := up.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != schemaVersion {
		t.Fatalf("user_version = %d, %v", v, err)
	}
	got := linkPairs(t, up, "sources/b.md")
	if len(got) != 2 {
		t.Fatalf("upgraded links = %+v", got)
	}
	for id, l := range want {
		if g := got[id]; g.BasisReceipt != l.BasisReceipt || g.ObservedAt != l.ObservedAt || g.ObjectID != l.ObjectID {
			t.Fatalf("upgraded link %s = %+v, want %+v", id, g, l)
		}
	}
}
