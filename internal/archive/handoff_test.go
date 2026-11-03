package archive

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var ctx = context.Background()

func verified(body, name, url string) SourceRequest {
	return SourceRequest{Content: strings.NewReader(body), OriginalName: name, URL: url, Title: "T", Completeness: "verified", Basis: "declared length matches"}
}

func mustCapture(t *testing.T, a *Archive, req SourceRequest) Result {
	t.Helper()
	res, err := a.CaptureSource(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func mustStatus(t *testing.T, a *Archive, id string) Capture {
	t.Helper()
	c, err := a.Status(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestRequestDigestAndIDUnchangedFromSchemaV1(t *testing.T) {
	a, _ := openTest(t)
	// IDs computed by the schema-1 code before summaries existed.
	src := mustCapture(t, a, SourceRequest{Content: strings.NewReader("pinned body\n"), OriginalName: "p.md", URL: "https://example.org/p", Title: "P", Supplier: "s"})
	if src.CaptureID != "cap_ef0da8d24f3fcd0b142dd4a2305f15ab" {
		t.Fatalf("source capture ID changed: %s", src.CaptureID)
	}
	rep, err := a.ImportReport(ctx, ReportRequest{Content: strings.NewReader("report\n"), OriginalName: "r.md", Title: "R", Cites: []string{src.CaptureID, "https://example.org/q"}})
	if err != nil {
		t.Fatal(err)
	}
	if rep.CaptureID != "cap_0b0666059570a9776d993b0287b4e131" {
		t.Fatalf("report capture ID changed: %s", rep.CaptureID)
	}
}

const v1DDL = `
CREATE TABLE objects (object_id TEXT PRIMARY KEY, size INTEGER NOT NULL, stored_at TEXT NOT NULL);
CREATE TABLE captures (capture_id TEXT PRIMARY KEY, request_key TEXT NOT NULL UNIQUE, request_digest TEXT NOT NULL,
 kind TEXT NOT NULL CHECK (kind IN ('source','report')), object_id TEXT NOT NULL REFERENCES objects(object_id),
 media_type TEXT NOT NULL, original_name TEXT NOT NULL, url TEXT NOT NULL, supplier TEXT NOT NULL, title TEXT NOT NULL,
 acquisition TEXT NOT NULL CHECK (acquisition IN ('acquired','partial')),
 completeness TEXT NOT NULL CHECK (completeness IN ('verified','unknown','incomplete')),
 completeness_basis TEXT NOT NULL, recorded_at TEXT NOT NULL);
CREATE INDEX captures_object ON captures(object_id);
CREATE TABLE citations (report_id TEXT NOT NULL REFERENCES captures(capture_id), position INTEGER NOT NULL,
 cited_capture_id TEXT REFERENCES captures(capture_id), cited_url TEXT NOT NULL, PRIMARY KEY (report_id, position),
 CHECK ((cited_capture_id IS NULL) <> (cited_url = '')));
CREATE INDEX citations_cited ON citations(cited_capture_id);
INSERT INTO objects VALUES ('sha256:` + "aa" + `', 3, '2026-01-01T00:00:00Z');
INSERT INTO captures VALUES ('cap_src', 'k1', 'd1', 'source', 'sha256:aa', 'text/plain', 's.md', 'https://e.org/s', '', 'S', 'acquired', 'unknown', '', '2026-01-01T00:00:00Z');
INSERT INTO captures VALUES ('cap_rep', 'k2', 'd2', 'report', 'sha256:aa', 'text/plain', 'r.md', '', '', 'R', 'acquired', 'unknown', '', '2026-01-01T00:00:01Z');
INSERT INTO citations VALUES ('cap_rep', 1, 'cap_src', '');
PRAGMA user_version = 1;
`

func TestSchemaV1RegistryMigrates(t *testing.T) {
	root := filepath.Join(t.TempDir(), "archive")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(root, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(v1DDL); err != nil {
		t.Fatal(err)
	}
	db.Close()
	a, err := Open(root)
	if err != nil {
		t.Fatalf("Open v1: %v", err)
	}
	defer a.Close()
	var v int
	if err := a.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil || v != schemaVersion {
		t.Fatalf("user_version = %d, %v", v, err)
	}
	c := mustStatus(t, a, "cap_src")
	if c.Completeness != "unknown" || len(c.CitedBy) != 1 || c.CitedBy[0] != "cap_rep" || c.Ingest.State != "held" {
		t.Fatalf("migrated status = %+v", c)
	}
	var fk int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM pragma_foreign_key_check`).Scan(&fk); err != nil || fk != 0 {
		t.Fatalf("foreign key violations = %d, %v", fk, err)
	}
	a.Close()
	if a, err = Open(root); err != nil {
		t.Fatalf("reopen after migration: %v", err)
	}
}

func TestSummaryIsNeverAnOriginal(t *testing.T) {
	a, _ := openTest(t)
	src := mustCapture(t, a, source("original text\n", "https://example.org/a"))
	before := mustStatus(t, a, src.CaptureID)

	sum, err := a.CaptureSummary(ctx, SummaryRequest{Content: strings.NewReader("short summary\n"), OriginalName: "s.md", SummaryOf: src.CaptureID, Title: "S"})
	if err != nil {
		t.Fatal(err)
	}
	s := mustStatus(t, a, sum.CaptureID)
	if s.Kind != "summary" || s.Acquisition != "not_applicable" || s.Completeness != "not_applicable" || s.SummaryOf != src.CaptureID || s.Ingest.State != "not_applicable" {
		t.Fatalf("summary status = %+v", s)
	}
	after := mustStatus(t, a, src.CaptureID)
	if after.Acquisition != before.Acquisition || after.Completeness != before.Completeness || after.ObjectID != before.ObjectID {
		t.Fatalf("summary changed the source: before %+v after %+v", before, after)
	}
	if len(after.Summaries) != 1 || after.Summaries[0] != sum.CaptureID {
		t.Fatalf("source summaries = %v", after.Summaries)
	}

	urlOnly, err := a.CaptureSummary(ctx, SummaryRequest{Content: strings.NewReader("summary of an uncaptured page\n"), OriginalName: "u.md", URL: "https://example.org/never-fetched"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := a.Handoff(ctx, HandoffExtensions)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range res {
		if r.CaptureID == sum.CaptureID || r.CaptureID == urlOnly.CaptureID {
			t.Fatalf("a summary was considered for hand-off: %+v", r)
		}
	}

	rep, err := a.ImportReport(ctx, ReportRequest{Content: strings.NewReader("report\n"), OriginalName: "r.md"})
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []SummaryRequest{
		{Content: strings.NewReader("x"), OriginalName: "x.md"},
		{Content: strings.NewReader("x"), OriginalName: "x.md", SummaryOf: rep.CaptureID},
		{Content: strings.NewReader("x"), OriginalName: "x.md", SummaryOf: sum.CaptureID},
		{Content: strings.NewReader("x"), OriginalName: "x.md", SummaryOf: "cap_00000000000000000000000000000000"},
	} {
		if _, err := a.CaptureSummary(ctx, bad); err == nil {
			t.Fatalf("summary %+v accepted", bad)
		}
	}
}

func TestRecoveryAttemptsAppendAndLinkWithoutReplacing(t *testing.T) {
	a, _ := openTest(t)
	partial := mustCapture(t, a, SourceRequest{Content: strings.NewReader("first half"), OriginalName: "a.md", URL: "https://example.org/a", Completeness: "incomplete"})

	failed, err := a.RecordAttempt(ctx, Attempt{CaptureID: partial.CaptureID, Cause: "truncated", Tried: "refetch https://example.org/a", Result: "failed", RemainingGap: "second half", NextAction: "ask the user for the file"})
	if err != nil {
		t.Fatal(err)
	}
	full := mustCapture(t, a, verified("first half, second half", "a.md", "https://example.org/a"))
	ok, err := a.RecordAttempt(ctx, Attempt{CaptureID: partial.CaptureID, Cause: "truncated", Tried: "user-supplied file", Result: "acquired", ResultCaptureID: full.CaptureID})
	if err != nil {
		t.Fatal(err)
	}

	old := mustStatus(t, a, partial.CaptureID)
	if old.Acquisition != "partial" || old.Completeness != "incomplete" {
		t.Fatalf("recovery replaced the partial capture: %+v", old)
	}
	if len(old.Attempts) != 2 || old.Attempts[0].AttemptID != failed.AttemptID || old.Attempts[1].AttemptID != ok.AttemptID {
		t.Fatalf("attempts = %+v", old.Attempts)
	}
	recovered := mustStatus(t, a, full.CaptureID)
	if len(recovered.RecoveredBy) != 1 || recovered.RecoveredBy[0].CaptureID != partial.CaptureID {
		t.Fatalf("recovered_by = %+v", recovered.RecoveredBy)
	}

	for _, bad := range []Attempt{
		{CaptureID: partial.CaptureID, Cause: "truncated", Tried: "x", Result: "acquired"},
		{CaptureID: partial.CaptureID, Cause: "truncated", Tried: "x", Result: "failed", ResultCaptureID: full.CaptureID},
		{CaptureID: partial.CaptureID, Cause: "truncated", Tried: "x", Result: "acquired", ResultCaptureID: partial.CaptureID},
		{CaptureID: partial.CaptureID, Cause: "guess", Tried: "x", Result: "failed"},
		{CaptureID: partial.CaptureID, Cause: "truncated", Tried: " ", Result: "failed"},
		{CaptureID: "cap_00000000000000000000000000000000", Cause: "truncated", Tried: "x", Result: "failed"},
	} {
		if _, err := a.RecordAttempt(ctx, bad); err == nil {
			t.Fatalf("attempt %+v accepted", bad)
		}
	}
}

func TestDecodeStrictRejectsTrailingValues(t *testing.T) {
	var m Manifest
	if err := decodeStrict([]byte(`{"version":1}`+"\n  \n"), &m); err != nil {
		t.Fatalf("trailing whitespace rejected: %v", err)
	}
	for _, in := range []string{`{"version":1}{"version":2}`, `{"version":1}}`, `{"version":1} 7`, `{"version":1,"extra":1}`, `{"version":1}]`} {
		if err := decodeStrict([]byte(in), &m); err == nil {
			t.Fatalf("%q accepted", in)
		}
	}
}

func handoffFixture(t *testing.T) (*Archive, string, string) {
	t.Helper()
	a, root := openTest(t)
	dest := filepath.Join(t.TempDir(), "sources")
	if err := os.Mkdir(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	return a, root, dest
}

func statuses(res []HandoffResult) map[string]HandoffResult {
	out := map[string]HandoffResult{}
	for _, r := range res {
		out[r.CaptureID] = r
	}
	return out
}

func TestHandoffHoldsUnverifiedAndUnsupportedTypes(t *testing.T) {
	a, root, _ := handoffFixture(t)
	v := mustCapture(t, a, verified("verified md\n", "v.md", "https://example.org/v"))
	u := mustCapture(t, a, source("unknown md\n", "https://example.org/u"))
	p := mustCapture(t, a, SourceRequest{Content: strings.NewReader("partial\n"), OriginalName: "p.md", Completeness: "incomplete"})
	txt := mustCapture(t, a, verified("plain text\n", "t.txt", ""))
	html := mustCapture(t, a, verified("<html></html>", "h.html", ""))

	res, err := a.Handoff(ctx, DefaultHandoffTypes)
	if err != nil {
		t.Fatal(err)
	}
	got := statuses(res)
	if got[v.CaptureID].Status != "written" || got[v.CaptureID].Extension != "md" {
		t.Fatalf("verified md: %+v", got[v.CaptureID])
	}
	for _, id := range []string{u.CaptureID, p.CaptureID, txt.CaptureID, html.CaptureID} {
		if got[id].Status != "held" || got[id].Reason == "" {
			t.Fatalf("%s: %+v, want held with a reason", id, got[id])
		}
		if _, err := os.Stat(filepath.Join(root, "outbox", id+".json")); !os.IsNotExist(err) {
			t.Fatalf("held capture %s has a manifest", id)
		}
	}
	if s := mustStatus(t, a, u.CaptureID); s.Ingest.State != "held" {
		t.Fatalf("unknown capture ingest view = %+v", s.Ingest)
	}
	if s := mustStatus(t, a, v.CaptureID); s.Ingest.State != "awaiting_ingest" {
		t.Fatalf("handed-off capture ingest view = %+v", s.Ingest)
	}

	// .txt is handed off only when the operator names it for a text backend.
	res, err = a.Handoff(ctx, HandoffExtensions)
	if err != nil {
		t.Fatal(err)
	}
	got = statuses(res)
	if got[txt.CaptureID].Status != "written" || got[v.CaptureID].Status != "existing" || got[u.CaptureID].Status != "held" {
		t.Fatalf("second hand-off: %+v", got)
	}

	manifest := filepath.Join(root, "outbox", v.CaptureID+".json")
	want, _ := os.ReadFile(manifest)
	os.Remove(manifest)
	if _, err := a.Handoff(ctx, DefaultHandoffTypes); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(manifest); string(again) != string(want) {
		t.Fatalf("rewritten manifest differs: %s vs %s", again, want)
	}
	if err := os.WriteFile(manifest+".tmp", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	os.Remove(manifest)
	if err := os.WriteFile(manifest, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Handoff(ctx, DefaultHandoffTypes); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed manifest: err = %v, want ErrConflict", err)
	}
	if _, err := a.Handoff(ctx, []string{"html"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unsupported type list: err = %v", err)
	}
}

func TestSameBytesWithDifferentNamesGetOneSourcePath(t *testing.T) {
	for _, order := range []string{"md first", "txt first"} {
		t.Run(order, func(t *testing.T) {
			a, root, dest := handoffFixture(t)
			body := "identical bytes\n"
			names := []string{"a.md", "b.txt", "c.html"}
			if order == "txt first" {
				names = []string{"c.html", "b.txt", "a.md"}
			}
			var ids []string
			for i, n := range names {
				ids = append(ids, mustCapture(t, a, verified(body, n, "https://example.org/"+string(rune('a'+i)))).CaptureID)
			}
			res, err := a.Handoff(ctx, HandoffExtensions)
			if err != nil {
				t.Fatal(err)
			}
			ext := ""
			for _, r := range res {
				if r.Status == "held" {
					t.Fatalf("same-bytes capture held: %+v", r)
				}
				if ext == "" {
					ext = r.Extension
				}
				if r.Extension != ext {
					t.Fatalf("one object handed off as .%s and .%s", ext, r.Extension)
				}
			}
			picked, err := Pickup(root, dest, HandoffExtensions)
			if err != nil {
				t.Fatal(err)
			}
			paths := map[string]bool{}
			for _, p := range picked {
				paths[p.SourcePath] = true
			}
			if len(picked) != len(ids) || len(paths) != 1 {
				t.Fatalf("pickup = %+v, want %d manifests on one path", picked, len(ids))
			}
			if n := sourceCopies(t, dest); n != 1 {
				t.Fatalf("destination holds %d copies", n)
			}
		})
	}
}

// sourceCopies counts files in dest other than the capture map.
func sourceCopies(t *testing.T, dest string) int {
	t.Helper()
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if e.Name() != CaptureMapName {
			n++
		}
	}
	return n
}

func TestPickupVerifiesAndNeverReplaces(t *testing.T) {
	a, root, dest := handoffFixture(t)
	md := mustCapture(t, a, verified("pickup body\n", "x.md", "https://example.org/x"))
	txt := mustCapture(t, a, verified("text body\n", "y.txt", "https://example.org/y"))
	if _, err := a.Handoff(ctx, HandoffExtensions); err != nil {
		t.Fatal(err)
	}

	// A path-input backend does not ingest .txt: refused, not copied.
	res, err := Pickup(root, dest, DefaultHandoffTypes)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]PickupResult{}
	for _, r := range res {
		got[r.CaptureID] = r
	}
	if got[md.CaptureID].Status != "copied" || got[txt.CaptureID].Status != "refused" {
		t.Fatalf("pickup = %+v", res)
	}
	if n := sourceCopies(t, dest); n != 1 {
		t.Fatalf("destination has %d copies, want only the md copy", n)
	}
	path := got[md.CaptureID].SourcePath
	if data, _ := os.ReadFile(path); string(data) != "pickup body\n" {
		t.Fatalf("copy = %q", data)
	}
	if res, err = Pickup(root, dest, DefaultHandoffTypes); err != nil || res[0].Status != "existing" && res[1].Status != "existing" {
		t.Fatalf("second pickup = %+v, %v", res, err)
	}

	os.Chmod(path, 0o644)
	os.WriteFile(path, []byte("changed locally\n"), 0o644)
	if _, err := Pickup(root, dest, DefaultHandoffTypes); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("changed copy: err = %v, want ErrIntegrity", err)
	}
	if data, _ := os.ReadFile(path); string(data) != "changed locally\n" {
		t.Fatal("pickup replaced an existing file")
	}
	os.Remove(path)

	obj := filepath.Join(root, "objects", "sha256", strings.TrimPrefix(md.ObjectID, "sha256:"))
	os.Chmod(obj, 0o644)
	os.WriteFile(obj, []byte("tampered!!!\n"), 0o644)
	if _, err := Pickup(root, dest, DefaultHandoffTypes); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("tampered object: err = %v, want ErrIntegrity", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("tampered object was copied")
	}

	if _, err := Pickup("relative", dest, DefaultHandoffTypes); err == nil {
		t.Fatal("relative archive root accepted")
	}
	link := filepath.Join(t.TempDir(), "link")
	os.Symlink(dest, link)
	if _, err := Pickup(root, link, DefaultHandoffTypes); err == nil {
		t.Fatal("symlinked destination accepted")
	}
}

func TestPickupRejectsMalformedOrConflictingManifests(t *testing.T) {
	a, root, dest := handoffFixture(t)
	res := mustCapture(t, a, verified("m body\n", "m.md", ""))
	if _, err := a.Handoff(ctx, DefaultHandoffTypes); err != nil {
		t.Fatal(err)
	}
	outbox := filepath.Join(root, "outbox")
	good, _ := os.ReadFile(filepath.Join(outbox, res.CaptureID+".json"))
	other := "cap_" + strings.Repeat("1", 32)
	conflict := strings.Replace(strings.Replace(string(good), res.CaptureID, other, 1), `"md"`, `"pdf"`, 1)
	for name, body := range map[string]string{
		"trailing":  string(good) + `{}`,
		"unknown":   strings.Replace(string(good), `{`, `{"path":"/etc/passwd",`, 1),
		"traversal": strings.Replace(string(good), `"md"`, `"../md"`, 1),
		"conflict":  conflict,
	} {
		t.Run(name, func(t *testing.T) {
			target := filepath.Join(outbox, other+".json")
			if name != "conflict" {
				target = filepath.Join(outbox, res.CaptureID+".json")
			}
			os.WriteFile(target, []byte(body), 0o600)
			defer os.WriteFile(filepath.Join(outbox, res.CaptureID+".json"), good, 0o600)
			defer os.Remove(filepath.Join(outbox, other+".json"))
			if _, err := Pickup(root, dest, HandoffExtensions); err == nil {
				t.Fatal("malformed hand-off accepted")
			}
		})
	}
}

type fakeLedger map[string]LedgerEntry

func (f fakeLedger) lookup(p, h string) (LedgerEntry, bool, error) {
	e, ok := f[p+"|"+h]
	return e, ok, nil
}

func TestReceiptsRecordLedgerObservationsOnly(t *testing.T) {
	a, root, dest := handoffFixture(t)
	res := mustCapture(t, a, verified("receipt body\n", "r.md", "https://example.org/r"))
	if _, err := a.Handoff(ctx, DefaultHandoffTypes); err != nil {
		t.Fatal(err)
	}
	picked, err := Pickup(root, dest, DefaultHandoffTypes)
	if err != nil {
		t.Fatal(err)
	}
	src := picked[0].SourcePath
	hexSum := strings.TrimPrefix(res.ObjectID, "sha256:")
	ledger := fakeLedger{}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	if out, err := WriteReceipts(root, dest, ledger.lookup, now); err != nil || len(out) != 0 {
		t.Fatalf("receipts without a ledger row = %+v, %v", out, err)
	}
	if s := mustStatus(t, a, res.CaptureID); s.Ingest.State != "awaiting_ingest" || s.Ingest.EvidenceRef != "" {
		t.Fatalf("before receipt: %+v", s.Ingest)
	}

	ledger[src+"|"+hexSum] = LedgerEntry{Status: "failed", WikiPage: "sources/r.md"}
	if out, err := WriteReceipts(root, dest, ledger.lookup, now); err != nil || len(out) != 1 {
		t.Fatalf("first receipt = %+v, %v", out, err)
	}
	if n, err := a.CollectReceipts(ctx); err != nil || n != 1 {
		t.Fatalf("collect first = %d, %v", n, err)
	}
	if v := mustStatus(t, a, res.CaptureID).Ingest; v.State != "observed" || v.LedgerStatus != "failed" || v.EvidenceRef != "" {
		t.Fatalf("failed observation = %+v; a failed ingest must not offer an evidence ref", v)
	}
	ledger[src+"|"+hexSum] = LedgerEntry{Status: "success", WikiPage: "sources/r.md", DigestedAt: "2026-01-02T03:04:00Z"}
	if out, _ := WriteReceipts(root, dest, ledger.lookup, now.Add(time.Minute)); len(out) != 1 {
		t.Fatalf("changed observation wrote %d receipts", len(out))
	}
	if out, _ := WriteReceipts(root, dest, ledger.lookup, now.Add(2*time.Minute)); len(out) != 0 {
		t.Fatalf("unchanged observation wrote %d receipts", len(out))
	}
	if n, err := a.CollectReceipts(ctx); err != nil || n != 1 {
		t.Fatalf("collect = %d, %v", n, err)
	}
	if n, err := a.CollectReceipts(ctx); err != nil || n != 0 {
		t.Fatalf("re-collect = %d, %v", n, err)
	}
	v := mustStatus(t, a, res.CaptureID).Ingest
	if v.State != "observed" || v.Source != "ingest_host_receipt" || v.LedgerStatus != "success" || v.EvidenceRef != "sources/r.md" || v.SourcePath != src || v.ObservedAt == "" {
		t.Fatalf("observed view = %+v", v)
	}

	first := filepath.Join(root, "receipts", res.CaptureID+".1.json")
	os.WriteFile(first, []byte(`{"tampered":true}`), 0o600)
	if _, err := a.CollectReceipts(ctx); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed collected receipt: err = %v", err)
	}
	os.Remove(first)

	held := mustCapture(t, a, source("never handed off\n", ""))
	forged := `{"version":1,"capture_id":"` + held.CaptureID + `","object_id":"` + held.ObjectID + `","source_path":"/x","content_hash":"` + strings.TrimPrefix(held.ObjectID, "sha256:") + `","ledger_status":"success","wiki_page":"sources/x.md","digested_at":"","observed_at":"2026-01-01T00:00:00Z"}`
	os.WriteFile(filepath.Join(root, "receipts", held.CaptureID+".1.json"), []byte(forged), 0o600)
	if _, err := a.CollectReceipts(ctx); !errors.Is(err, ErrInvalid) {
		t.Fatalf("receipt for a capture never handed off: err = %v", err)
	}
	os.Remove(filepath.Join(root, "receipts", held.CaptureID+".1.json"))

	bad := strings.Replace(forged, held.CaptureID, res.CaptureID, 1)
	bad = strings.Replace(strings.Replace(bad, held.ObjectID, res.ObjectID, 1), strings.TrimPrefix(held.ObjectID, "sha256:"), hexSum, 1)
	bad = strings.Replace(bad, "sources/x.md", "../outside.md", 1)
	os.WriteFile(filepath.Join(root, "receipts", res.CaptureID+".9.json"), []byte(bad), 0o600)
	if _, err := a.CollectReceipts(ctx); !errors.Is(err, ErrInvalid) {
		t.Fatalf("receipt with a non-wiki page: err = %v", err)
	}
}

func TestValidWikiPage(t *testing.T) {
	for p, want := range map[string]bool{
		"sources/a.md": true, "a.md": true, "": false, "/abs/a.md": false, "../a.md": false,
		"sources/../a.md": false, "sources/a.txt": false, `sources\a.md`: false,
	} {
		if got := ValidWikiPage(p); got != want {
			t.Errorf("ValidWikiPage(%q) = %v, want %v", p, got, want)
		}
	}
}
