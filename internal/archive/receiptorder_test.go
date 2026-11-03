package archive

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestReceiptOrderIsTheSequenceNumberNotNamesOrClocks: the receipt number
// in the file name is the only observation order. Names sort .10 before .2,
// and the ingest host's clock can go backwards (observed_at strings with
// and without fractions also sort out of time order).
func TestReceiptOrderIsTheSequenceNumberNotNamesOrClocks(t *testing.T) {
	a, root, _ := handoffFixture(t)
	res := mustCapture(t, a, verified("ordered\n", "o.md", "https://example.org/o"))
	if _, err := a.Handoff(ctx, DefaultHandoffTypes); err != nil {
		t.Fatal(err)
	}
	put := func(n int, status, observed, digested string) {
		t.Helper()
		r := Receipt{Version: receiptVersion, CaptureID: res.CaptureID, ObjectID: res.ObjectID, SourcePath: "/src/o.md",
			ContentHash: hexOf(res.ObjectID), LedgerStatus: status, WikiPage: "sources/o.md", DigestedAt: digested, ObservedAt: observed}
		body, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "receipts", fmt.Sprintf("%s.%d.json", res.CaptureID, n)), body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	put(1, "failed", "2026-05-01T00:00:03Z", "")
	put(2, "success", "2026-05-01T00:00:02.5Z", "d2")
	put(10, "success", "2026-05-01T00:00:02Z", "d10")
	if n, err := a.CollectReceipts(ctx); err != nil || n != 3 {
		t.Fatalf("collect = %d, %v", n, err)
	}
	link := func(a *Archive) PageLink {
		t.Helper()
		s := mustStatus(t, a, res.CaptureID)
		if len(s.PageLinks) != 1 {
			t.Fatalf("links = %+v", s.PageLinks)
		}
		return s.PageLinks[0]
	}
	if l := link(a); l.BasisReceipt != res.CaptureID+".2.json" || l.DigestedAt != "d2" || !l.Current {
		t.Fatalf("basis after .1/.2/.10 = %+v; want .2", l)
	}

	// .11 is the newest observation although its clock reads earlier.
	put(11, "failed", "2026-04-30T00:00:00Z", "")
	if _, err := a.CollectReceipts(ctx); err != nil {
		t.Fatal(err)
	}
	if v := mustStatus(t, a, res.CaptureID).Ingest; v.LedgerStatus != "failed" || v.EvidenceRef != "" {
		t.Fatalf("ingest view after a backwards clock = %+v; want .11 (failed)", v)
	}
	if l := link(a); l.Current || l.BasisReceipt != res.CaptureID+".2.json" {
		t.Fatalf("link after .11 = %+v", l)
	}

	// The basis rule does not depend on link order: linking .10 before .2
	// still leaves .2 as the basis.
	if _, err := a.db.Exec(`DELETE FROM page_links`); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{10, 2, 10} {
		var body string
		name := fmt.Sprintf("%s.%d.json", res.CaptureID, n)
		if err := a.db.QueryRow(`SELECT body FROM receipts WHERE name = ?`, name).Scan(&body); err != nil {
			t.Fatal(err)
		}
		var r Receipt
		if err := decodeStrict([]byte(body), &r); err != nil {
			t.Fatal(err)
		}
		if err := linkReceipt(ctx, a.db, name, r, "t"); err != nil {
			t.Fatal(err)
		}
	}
	if l := link(a); l.BasisReceipt != res.CaptureID+".2.json" || l.DigestedAt != "d2" {
		t.Fatalf("basis after linking .10 first = %+v", l)
	}

	// A version 2 registry is backfilled with the same contract.
	if _, err := a.db.Exec(`DROP TABLE page_links; PRAGMA user_version = 2`); err != nil {
		t.Fatal(err)
	}
	a.Close()
	up, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer up.Close()
	if l := link(up); l.BasisReceipt != res.CaptureID+".2.json" || l.Current {
		t.Fatalf("backfilled link = %+v", l)
	}
	if v := mustStatus(t, up, res.CaptureID).Ingest; v.LedgerStatus != "failed" {
		t.Fatalf("backfilled ingest view = %+v", v)
	}

	// A version 3 registry recorded before the order contract can hold a
	// .10 basis although .2 was already collected. Collection skips
	// collected receipts, so only the migration can repair it.
	if _, err := up.db.Exec(`UPDATE page_links SET basis_receipt = ?, digested_at = 'd10'; PRAGMA user_version = 3`, res.CaptureID+".10.json"); err != nil {
		t.Fatal(err)
	}
	if l := link(up); l.BasisReceipt != res.CaptureID+".10.json" {
		t.Fatalf("fixture: seeded basis = %+v", l)
	}
	if n, err := up.CollectReceipts(ctx); err != nil || n != 0 {
		t.Fatalf("re-collect = %d, %v", n, err)
	}
	if l := link(up); l.BasisReceipt != res.CaptureID+".10.json" {
		t.Fatalf("fixture: collection should not repair = %+v", l)
	}
	up.Close()
	fixed, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer fixed.Close()
	if l := link(fixed); l.BasisReceipt != res.CaptureID+".2.json" || l.DigestedAt != "d2" || l.Current {
		t.Fatalf("v3 link after reopen = %+v; want basis .2", l)
	}
	var v int
	if err := fixed.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != schemaVersion {
		t.Fatalf("user_version = %d, %v", v, err)
	}
}

func TestReceiptLess(t *testing.T) {
	c := "cap_" + fmt.Sprintf("%032x", 1)
	d := "cap_" + fmt.Sprintf("%032x", 2)
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{c + ".2.json", c + ".10.json", true},
		{c + ".10.json", c + ".2.json", false},
		{c + ".9.json", c + ".9.json", false},
		{c + ".10.json", d + ".1.json", true},
	} {
		if got := receiptLess(tc.a, tc.b); got != tc.want {
			t.Fatalf("receiptLess(%s, %s) = %v", tc.a, tc.b, got)
		}
	}
}
