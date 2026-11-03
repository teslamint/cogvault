package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/teslamint/cogvault/internal/archive"
	"github.com/teslamint/cogvault/internal/terms"
)

func backdateAll(t *testing.T, dir string) {
	t.Helper()
	old := time.Now().Add(-5 * time.Minute)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.Chtimes(filepath.Join(dir, e.Name()), old, old); err != nil {
			t.Fatal(err)
		}
	}
}

// TestArchiveHandoffRoundTripThroughRealIngest drives the hand-off with the
// real CLI on both sides and a real ingest run (fake claude binary): the
// archive host hands off a verified original, the ingest host copies it into
// a configured source directory, ingest digests it, the ingest host writes a
// receipt from its ledger read-only, and the archive host shows the
// observation and the source page as the evidence ref.
func TestArchiveHandoffRoundTripThroughRealIngest(t *testing.T) {
	fakeClaudeOnPath(t)
	t.Setenv("CLAUDE_FAKE_MODE", "ok")
	configPath, srcDir, wikiDir, _ := setupIngestVault(t)
	cfg := strings.Replace(mustRead(t, configPath), "types: [pdf]", "types: [pdf, md, txt]", 1)
	if err := os.WriteFile(configPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "archive")
	files := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(files, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	captureID := func(out string) string { return strings.Fields(out)[1] }

	out, _, err := executeCommand("archive", "--root", root, "capture", "--file", write("paper.pdf", "%PDF-1.4 archive fixture"), "--completeness", "verified", "--basis", "page count matches the table of contents")
	if err != nil {
		t.Fatal(err)
	}
	pdf := captureID(out)
	out, _, err = executeCommand("archive", "--root", root, "capture", "--file", write("notes.txt", "plain text original"), "--completeness", "verified", "--basis", "byte length declared by the supplier")
	if err != nil {
		t.Fatal(err)
	}
	txt := captureID(out)
	out, _, err = executeCommand("archive", "--root", root, "capture", "--file", write("unknown.md", "maybe partial"))
	if err != nil {
		t.Fatal(err)
	}
	unknown := captureID(out)

	out, _, err = executeCommand("archive", "--root", root, "handoff", "--types", "pdf,md,markdown,txt")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "written  "+pdf) || !strings.Contains(out, "written  "+txt) || !strings.Contains(out, "held     "+unknown) {
		t.Fatalf("handoff output:\n%s", out)
	}

	// claudecode is a path-input backend: .txt is refused even though the
	// source directory lists it.
	out, _, err = executeCommand("archive", "--root", root, "--config", configPath, "pickup", "--dest", srcDir, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var picked []archive.PickupResult
	if err := json.Unmarshal([]byte(out), &picked); err != nil {
		t.Fatal(err)
	}
	byID := map[string]archive.PickupResult{}
	for _, p := range picked {
		byID[p.CaptureID] = p
	}
	if byID[pdf].Status != "copied" || byID[txt].Status != "refused" {
		t.Fatalf("pickup = %+v", picked)
	}
	if _, _, err := executeCommand("archive", "--root", root, "--config", configPath, "pickup", "--dest", t.TempDir()); err == nil {
		t.Fatal("pickup into an unconfigured directory succeeded")
	}
	backdateAll(t, srcDir)

	out, _, err = executeCommand("archive", "--root", root, "--config", configPath, "receipts", "--dest", srcDir)
	if err == nil {
		t.Fatalf("receipts before any ingest run succeeded: %s", out)
	}
	if _, _, err := executeCommand("ingest", "--config", configPath); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	out, _, err = executeCommand("archive", "--root", root, "--config", configPath, "receipts", "--dest", srcDir)
	if err != nil || !strings.Contains(out, "wrote 1 receipt") {
		t.Fatalf("receipts: %q %v", out, err)
	}
	if out, _, err = executeCommand("archive", "--root", root, "collect-receipts"); err != nil || !strings.Contains(out, "collected 1") {
		t.Fatalf("collect: %q %v", out, err)
	}

	out, _, err = executeCommand("archive", "--root", root, "status", pdf, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var c archive.Capture
	if err := json.Unmarshal([]byte(out), &c); err != nil {
		t.Fatal(err)
	}
	if c.Ingest.State != "observed" || c.Ingest.LedgerStatus != "success" || c.Ingest.SourcePath != byID[pdf].SourcePath || !archive.ValidWikiPage(c.Ingest.EvidenceRef) {
		t.Fatalf("ingest view = %+v", c.Ingest)
	}
	if _, err := os.Stat(filepath.Join(wikiDir, c.Ingest.EvidenceRef)); err != nil {
		t.Fatalf("evidence ref %s is not a wiki page: %v", c.Ingest.EvidenceRef, err)
	}
	out, _, _ = executeCommand("archive", "--root", root, "status", txt)
	if !strings.Contains(out, "ingest        awaiting_ingest") {
		t.Fatalf("refused .txt status:\n%s", out)
	}

	// Ingest wrote the capture map's IDs on the source page.
	pagePath := filepath.Join(wikiDir, c.Ingest.EvidenceRef)
	meta, _, err := terms.Parse([]byte(mustRead(t, pagePath)))
	if err != nil || !archive.PageRecordsCapture(meta, pdf, c.ObjectID) {
		t.Fatalf("source page frontmatter does not record %s: %v %v", pdf, meta, err)
	}

	// A later capture of the same bytes: ingest does not rewrite the page (a
	// user edit made meanwhile stays), and the archive records the link
	// from the ingest host's receipt instead.
	out, _, err = executeCommand("archive", "--root", root, "capture", "--file", write("paper-mirror.pdf", "%PDF-1.4 archive fixture"), "--url", "https://mirror.example.org/p", "--completeness", "verified", "--basis", "same bytes")
	if err != nil {
		t.Fatal(err)
	}
	mirror := captureID(out)
	if _, _, err := executeCommand("archive", "--root", root, "handoff"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := executeCommand("archive", "--root", root, "--config", configPath, "pickup", "--dest", srcDir); err != nil {
		t.Fatal(err)
	}
	if _, ids, _ := archive.CapturesForSource(byID[pdf].SourcePath, strings.TrimPrefix(c.ObjectID, "sha256:")); len(ids) != 2 {
		t.Fatalf("capture map = %v, want both captures", ids)
	}
	// The user edits the page between the digest and the next run.
	edited := mustRead(t, pagePath) + "\nUser note kept.\n"
	if err := os.WriteFile(pagePath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, _, err := executeCommand("ingest", "--config", configPath); err != nil || !strings.Contains(out, "unchanged=1") {
		t.Fatalf("second ingest: %q %v", out, err)
	}
	if out, _, err := executeCommand("archive", "--root", root, "--config", configPath, "receipts", "--dest", srcDir); err != nil || !strings.Contains(out, "wrote 1 receipt") {
		t.Fatalf("receipts for the mirror: %q %v", out, err)
	}
	if out, _, err := executeCommand("archive", "--root", root, "collect-receipts"); err != nil || !strings.Contains(out, "collected 1") {
		t.Fatalf("collect: %q %v", out, err)
	}
	if mustRead(t, pagePath) != edited {
		t.Fatal("the page changed; the user edit must stay")
	}
	out, _, err = executeCommand("archive", "--root", root, "page-links", c.Ingest.EvidenceRef, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var links []archive.PageLink
	if err := json.Unmarshal([]byte(out), &links); err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 || links[0].CaptureID != pdf || links[1].CaptureID != mirror || links[0].BasisReceipt == links[1].BasisReceipt {
		t.Fatalf("page links = %+v", links)
	}
	for _, l := range links {
		if l.ObjectID != c.ObjectID || !l.Current || l.WikiPage != c.Ingest.EvidenceRef {
			t.Fatalf("link = %+v", l)
		}
	}
	// The frontmatter stays the digest-time snapshot: it lists only the
	// first capture.
	meta, _, _ = terms.Parse([]byte(edited))
	if archive.PageRecordsCapture(meta, mirror, c.ObjectID) || !archive.PageRecordsCapture(meta, pdf, c.ObjectID) {
		t.Fatalf("frontmatter snapshot changed: %v", meta)
	}
	if out, _, err := executeCommand("archive", "--root", root, "page-links", "../outside.md"); err == nil {
		t.Fatalf("page-links accepted a non-wiki path: %s", out)
	}
}

// TestIngestSetsArchiveKeysOnlyFromMatchingCaptureMap: the model's own
// archive keys never survive, and a map entry for other bytes records
// nothing.
func TestIngestSetsArchiveKeysOnlyFromMatchingCaptureMap(t *testing.T) {
	fakeClaudeOnPath(t)
	t.Setenv("CLAUDE_FAKE_MODE", "spoof_archive")
	configPath, srcDir, wikiDir, _ := setupIngestVault(t)
	writeAgedSource(t, srcDir, "mapped.pdf", "mapped bytes")
	writeAgedSource(t, srcDir, "stale.pdf", "edited bytes")
	const realID, staleID = "cap_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "cap_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	writeCaptureMap(t, srcDir, map[string]archive.CaptureMapEntry{
		"mapped.pdf": {ObjectID: objectIDOf("mapped bytes"), CaptureIDs: []string{realID}},
		"stale.pdf":  {ObjectID: objectIDOf("original bytes"), CaptureIDs: []string{staleID}},
	})
	if out, _, err := executeCommand("ingest", "--config", configPath); err != nil || !strings.Contains(out, "digested=2") {
		t.Fatalf("ingest: %q %v", out, err)
	}
	mapped, _, _ := terms.Parse([]byte(mustRead(t, sourcePage(t, wikiDir, "mapped"))))
	if !archive.PageRecordsCapture(mapped, realID, objectIDOf("mapped bytes")) || archive.PageRecordsCapture(mapped, "cap_0123456789abcdef0123456789abcdef", objectIDOf("mapped bytes")) {
		t.Fatalf("mapped page = %v", mapped)
	}
	stale, _, _ := terms.Parse([]byte(mustRead(t, sourcePage(t, wikiDir, "stale"))))
	if _, has := stale[archive.PageCaptureIDsKey]; has {
		t.Fatalf("page for changed bytes kept archive keys: %v", stale)
	}
	if _, has := stale[archive.PageObjectIDKey]; has {
		t.Fatalf("page for changed bytes kept archive keys: %v", stale)
	}
}

func objectIDOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeCaptureMap(t *testing.T, dir string, entries map[string]archive.CaptureMapEntry) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"version": 1, "entries": entries})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, archive.CaptureMapName), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// sourcePage returns the one source page ingest wrote for a source stem.
func sourcePage(t *testing.T, wikiDir, stem string) string {
	t.Helper()
	matches, _ := filepath.Glob(filepath.Join(wikiDir, "sources", stem+"*.md"))
	if len(matches) != 1 {
		t.Fatalf("source pages for %s = %v", stem, matches)
	}
	return matches[0]
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
