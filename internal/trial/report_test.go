package trial

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/teslamint/cogvault/internal/archive"
)

func manifestFixture() SourceManifest {
	return SourceManifest{Version: 1, ReportID: "libfile_example", SelectedSHA256: "sha256:" + strings.Repeat("a", 64), Sources: []ManifestSource{
		{ID: "public", CitationURL: "https://pdf.test/article", AcquireURL: "https://pdf.test/a.pdf", Relation: "body_url", CitationCount: 2, Mode: "http"},
		{ID: "kci", CitationURL: "https://www.kci.go.kr/kciportal/ci/sereArticleSearch/ciSereArtiView.kci?sereArticleSearchBean.artiId=ART002605798", Relation: "citation", CitationCount: 1, Mode: "manual", ExternalOriginal: &ExternalOriginal{SHA256: "sha256:" + strings.Repeat("b", 64), Size: 606425, Verification: "verified"}},
	}}
}
func manifestJSON(t *testing.T, m SourceManifest) string {
	t.Helper()
	b, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
func reportService(t *testing.T) *Service {
	t.Helper()
	s, e := Open(mustConfig(t, newRoot(t), nil))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func importFixture(t *testing.T, s *Service) archive.Result {
	t.Helper()
	r, e := s.ImportManifest(ctx, manifestJSON(t, manifestFixture()))
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func TestManifestStrictAndNoNetwork(t *testing.T) {
	s := reportService(t)
	calls := 0
	s.Acquirer = archive.Acquirer{Resolve: func(context.Context, string) ([]netip.Addr, error) { calls++; return nil, errors.New("no network") }, Dial: func(context.Context, string, string) (net.Conn, error) { calls++; return nil, errors.New("no network") }}
	raw := manifestJSON(t, manifestFixture())
	bad := []string{raw + ` {}`, strings.Replace(raw, `"version":1`, `"version":1,"body":"private report"`, 1), strings.Replace(raw, `"version":1`, `"version":1,"version":1`, 1), strings.Replace(raw, `"version":1`, `"Version":1`, 1), strings.Repeat(" ", 8193), "null", raw + "x"}
	for _, r := range bad {
		if _, e := s.ImportManifest(ctx, r); e == nil {
			t.Fatalf("accepted invalid %.100s", r)
		}
	}
	mutations := []func(*SourceManifest){
		func(m *SourceManifest) { m.Version = 2 }, func(m *SourceManifest) { m.ReportID = "/secret/path" }, func(m *SourceManifest) { m.SelectedSHA256 = "bad" },
		func(m *SourceManifest) { m.Sources = append(m.Sources, m.Sources[0]) }, func(m *SourceManifest) { m.Sources = nil },
		func(m *SourceManifest) { m.Sources[0].CitationURL = "https://example.org/?token=secret" }, func(m *SourceManifest) { m.Sources[0].CitationURL = "file:///etc/passwd" },
		func(m *SourceManifest) { m.Sources[0].CitationURL = "https://user:secret@example.org/a" }, func(m *SourceManifest) { m.Sources[0].CitationURL = "https://127.0.0.1/a" },
		func(m *SourceManifest) { m.Sources[0].AcquireURL = "https://pdf.test/a.pdf?secret=x" }, func(m *SourceManifest) { m.Sources[1].CitationCount = 0 },
		func(m *SourceManifest) { m.Sources[0].Mode = "browser" }, func(m *SourceManifest) { m.Sources[0].Relation = "other" },
		func(m *SourceManifest) { m.Sources[1].ExternalOriginal.SHA256 = "/private/file" }, func(m *SourceManifest) {
			m.Sources[1].Mode = "http"
			m.Sources[1].AcquireURL = m.Sources[1].CitationURL
		},
	}
	for i, f := range mutations {
		m := manifestFixture()
		f(&m)
		if _, e := s.ImportManifest(ctx, manifestJSON(t, m)); e == nil {
			t.Fatalf("accepted mutation %d", i)
		}
	}
	r := importFixture(t, s)
	again, e := s.ImportManifest(ctx, " \n"+raw)
	if e != nil || !again.Existing || r.CaptureID != again.CaptureID {
		t.Fatalf("idempotence %v %v", again, e)
	}
	m := manifestFixture()
	m.Sources[0].CitationCount++
	if _, e := s.ImportManifest(ctx, manifestJSON(t, m)); !errors.Is(e, archive.ErrConflict) {
		t.Fatalf("immutable %v", e)
	}
	activity, e := s.A.JobActivity(ctx, time.Time{})
	if e != nil || activity.Created != 0 || calls != 0 {
		t.Fatalf("import side effects %+v calls=%d %v", activity, calls, e)
	}
}

func TestManifestRestartPolicyAndManualReference(t *testing.T) {
	s := reportService(t)
	r := importFixture(t, s)
	status, e := s.ReportStatus(ctx, r.CaptureID)
	if e != nil {
		t.Fatal(e)
	}
	if status.Sources[0].State != "policy_blocked" || status.Sources[1].State != "manual" || status.Sources[1].ExternalOriginal.Preservation != "not_preserved_in_trial" {
		t.Fatalf("status %+v", status)
	}
	if _, _, e := s.SubmitSource(ctx, "https://pdf.test/a.pdf", "test", r.CaptureID, "public"); !errors.Is(e, ErrPolicy) {
		t.Fatalf("policy %v", e)
	}
	s.Cfg.AllowedURLs = []string{"https://pdf.test/a.pdf"}
	if _, _, e := s.SubmitSource(ctx, "https://pdf.test/other.pdf", "test", r.CaptureID, "public"); !errors.Is(e, ErrPolicy) {
		t.Fatalf("mismatch %v", e)
	}
	if _, _, e := s.SubmitSource(ctx, manifestFixture().Sources[1].CitationURL, "test", r.CaptureID, "kci"); !errors.Is(e, ErrPolicy) {
		t.Fatalf("manual %v", e)
	}
	j, existing, e := s.SubmitSource(ctx, "https://pdf.test/a.pdf", "test", "libfile_example", "public")
	if e != nil || existing || j.ReportCaptureID != r.CaptureID || j.ClaimRef != "source:public" || j.SeenSHA256 != "" {
		t.Fatalf("job %+v %v", j, e)
	}
	if e := s.validateReportJob(ctx, &j); e != nil {
		t.Fatal(e)
	}
	bad := j
	bad.ClaimRef = "source:kci"
	if e := s.validateReportJob(ctx, &bad); !errors.Is(e, ErrPolicy) {
		t.Fatalf("manual worker %v", e)
	}
	bad = j
	bad.SeenSHA256 = "sha256:" + strings.Repeat("c", 64)
	if e := s.validateReportJob(ctx, &bad); !errors.Is(e, ErrPolicy) {
		t.Fatalf("forged seen %v", e)
	}
	cfg := s.Cfg
	s.Close()
	again, e := Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer again.Close()
	got, e := again.ReportStatus(ctx, "libfile_example")
	if e != nil || got.CaptureID != r.CaptureID || got.Sources[0].State != "queued" {
		t.Fatalf("restart %+v %v", got, e)
	}
	if _, e := s.ReportStatus(ctx, "../secret"); e == nil {
		t.Fatal("path lookup accepted")
	}
}

func originalFixture(body, media, completeness string) OriginalImport {
	sum := sha256.Sum256([]byte(body))
	return OriginalImport{Content: strings.NewReader(body), ExpectedSHA256: "sha256:" + hex.EncodeToString(sum[:]), ExpectedSize: int64(len(body)), MediaType: media, Completeness: completeness, AcquisitionMethod: "existing_file"}
}

func TestImportOriginalHashesMediaAndMultiCaptureStatus(t *testing.T) {
	s := reportService(t)
	r := importFixture(t, s)
	for i, change := range []func(*OriginalImport){
		func(r *OriginalImport) { r.ExpectedSHA256 = "sha256:" + strings.Repeat("c", 64) }, func(r *OriginalImport) { r.ExpectedSize++ },
		func(r *OriginalImport) { r.MediaType = "text/html" }, func(r *OriginalImport) { r.Completeness = "verified" }, func(r *OriginalImport) { r.AcquisitionMethod = "/private" },
		func(r *OriginalImport) { r.ExpectedSize = archive.MaxObjectBytes + 1 }, func(r *OriginalImport) { r.Content = nil },
	} {
		v := originalFixture(pdfBody, "application/pdf", "unknown")
		change(&v)
		if _, e := s.ImportOriginal(ctx, r.CaptureID, "kci", v); e == nil {
			t.Fatalf("accepted original %d", i)
		}
	}
	pdf, e := s.ImportOriginal(ctx, r.CaptureID, "kci", originalFixture(pdfBody, "application/pdf", "unknown"))
	if e != nil {
		t.Fatal(e)
	}
	duplicate, e := s.ImportOriginal(ctx, r.CaptureID, "kci", originalFixture(pdfBody, "application/pdf", "unknown"))
	if e != nil || !duplicate.Existing || duplicate.CaptureID != pdf.CaptureID {
		t.Fatalf("duplicate %+v %v", duplicate, e)
	}
	if _, e := s.ImportOriginal(ctx, r.CaptureID, "kci", originalFixture("<!doctype html><html>partial</html>", "text/html", "incomplete")); e != nil {
		t.Fatal(e)
	}
	got, e := s.ReportStatus(ctx, r.CaptureID)
	if e != nil {
		t.Fatal(e)
	}
	if len(got.Sources[1].Captures) != 2 {
		t.Fatalf("captures %+v", got)
	}
	for _, c := range got.Sources[1].Captures {
		if c.Provenance != "imported_existing" || c.Completeness == "verified" || c.AcquisitionMethod != "existing_file" {
			t.Fatalf("misrepresented %+v", c)
		}
	}
	if got.Aggregate.StoredCaptures != 2 {
		t.Fatalf("aggregate %+v", got.Aggregate)
	}
	if _, e := s.ImportOriginal(ctx, r.CaptureID, "absent", originalFixture(pdfBody, "application/pdf", "unknown")); e == nil {
		t.Fatal("missing source")
	}
}

func TestManifestTamperAndForeignReportRefused(t *testing.T) {
	s := reportService(t)
	r := importFixture(t, s)
	foreign, e := s.A.ImportReport(ctx, archive.ReportRequest{Content: strings.NewReader(manifestJSON(t, manifestFixture())), Supplier: "not trial"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e := s.ReportStatus(ctx, foreign.CaptureID); !errors.Is(e, archive.ErrNotFound) {
		t.Fatalf("foreign %v", e)
	}
	p := filepath.Join(s.Cfg.Root, "objects", "sha256", strings.TrimPrefix(r.ObjectID, "sha256:"))
	if e := os.Chmod(p, 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(p, []byte("tamper"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := s.ReportStatus(ctx, r.CaptureID); !errors.Is(e, archive.ErrIntegrity) {
		t.Fatalf("tamper %v", e)
	}
}

func TestReportStatusNeverEchoesJobSecrets(t *testing.T) {
	s := reportService(t)
	r := importFixture(t, s)
	s.Cfg.AllowedURLs = []string{"https://pdf.test/a.pdf"}
	j, _, e := s.SubmitSource(ctx, "https://pdf.test/a.pdf", "SECRET submitter", r.CaptureID, "public")
	if e != nil {
		t.Fatal(e)
	}
	claimed, e := s.A.ClaimJob(ctx, archive.ExecutorHTTP, "SECRET worker", time.Minute)
	if e != nil || claimed == nil {
		t.Fatal(e)
	}
	if _, e := s.A.CompleteJob(ctx, j.JobID, "SECRET worker", claimed.AttemptsMade, archive.JobOutcome{State: archive.JobFailed, Detail: "SECRET https://secret/?token=secret /private", Response: "SECRET headers"}); e != nil {
		t.Fatal(e)
	}
	got, e := s.ReportStatus(ctx, r.CaptureID)
	if e != nil {
		t.Fatal(e)
	}
	b, _ := json.Marshal(got)
	if bytes.Contains(b, []byte("SECRET")) || bytes.Contains(b, []byte("token=")) || bytes.Contains(b, []byte("/private")) {
		t.Fatalf("leak %s", b)
	}
	if got.Sources[0].FailureReason != "collection_failed" {
		t.Fatalf("reason %+v", got.Sources[0])
	}
}

// Compile-time check: the operator service accepts streams, never file paths.
var _ io.Reader = strings.NewReader("")

func TestManifestSourceShapesAndExplicitCounts(t *testing.T) {
	s := reportService(t)
	m := manifestFixture()
	m.Sources[0].Relation = "citation"
	if _, e := s.ImportManifest(ctx, manifestJSON(t, m)); e != nil {
		t.Fatalf("separate acquire URL: %v", e)
	}
	m.ReportID = "body-url"
	m.Sources[0].Relation = "body_url"
	m.Sources[0].CitationCount = 0
	m.Sources[0].AcquireURL = ""
	raw := manifestJSON(t, m)
	if _, e := s.ImportManifest(ctx, raw); e != nil {
		t.Fatalf("bare URL with no formal citations: %v", e)
	}
	for _, bad := range []string{
		strings.Replace(raw, `"citation_count":0,`, "", 1),
		strings.Replace(raw, `"citation_count":0`, `"citation_count":null`, 1),
		strings.Replace(raw, `"id":"public"`, `"id":"public","id":"other"`, 1),
		strings.Replace(raw, `"id":"public"`, `"id":"public","path":"/private"`, 1),
		strings.Replace(raw, `"size":606425`, `"size":606425,"approved":true`, 1),
	} {
		if _, e := s.ImportManifest(ctx, bad); e == nil {
			t.Fatalf("accepted %.180s", bad)
		}
	}
	m.ReportID = "too-many"
	for len(m.Sources) <= MaxManifestSources {
		x := m.Sources[0]
		x.ID += "-" + strings.Repeat("x", len(m.Sources))
		m.Sources = append(m.Sources, x)
	}
	if _, e := s.ImportManifest(ctx, manifestJSON(t, m)); e == nil {
		t.Fatal("source cap ignored")
	}
	if !safeManifestURL("https://www.bok.or.kr/portal/cmmn/file/fileDown.do?atchFileId=4826d55c88464526b2c36e125afd84fd&fileSn=2") {
		t.Fatal("public BOK query rejected")
	}
	for _, u := range []string{"https://www.bok.or.kr/portal/cmmn/file/fileDown.do?atchFileId=4826d55c88464526b2c36e125afd84fd&fileSn=2&token=secret", "https://www.kci.go.kr/kciportal/ci/sereArticleSearch/ciSereArtiView.kci?sereArticleSearchBean.artiId=ART002605798&token=x", "https://pdf.test/a#token", "https://pdf.test/a?", "https://pdf.test/%2fsecret"} {
		if safeManifestURL(u) {
			t.Fatalf("unsafe URL %s", u)
		}
	}
}

func TestReportAndOriginalCapsKeepDuplicatesIdempotent(t *testing.T) {
	s := reportService(t)
	r := importFixture(t, s)
	for i := 1; i < MaxTrialReports; i++ {
		m := manifestFixture()
		m.ReportID = "report-" + strings.Repeat("x", i)
		if _, e := s.ImportManifest(ctx, manifestJSON(t, m)); e != nil {
			t.Fatal(e)
		}
	}
	if dup, e := s.ImportManifest(ctx, manifestJSON(t, manifestFixture())); e != nil || !dup.Existing {
		t.Fatalf("duplicate at cap %v %v", dup, e)
	}
	m := manifestFixture()
	m.ReportID = "over-cap"
	if _, e := s.ImportManifest(ctx, manifestJSON(t, m)); !errors.Is(e, ErrLimit) {
		t.Fatalf("report cap %v", e)
	}
	for i := 0; i < MaxSourceOriginals; i++ {
		if _, e := s.ImportOriginal(ctx, r.CaptureID, "kci", originalFixture(pdfBody+strings.Repeat("x", i), "application/pdf", "unknown")); e != nil {
			t.Fatal(e)
		}
	}
	if dup, e := s.ImportOriginal(ctx, r.CaptureID, "kci", originalFixture(pdfBody, "application/pdf", "unknown")); e != nil || !dup.Existing {
		t.Fatalf("original duplicate at cap %v %v", dup, e)
	}
	if _, e := s.ImportOriginal(ctx, r.CaptureID, "kci", originalFixture(pdfBody+"overflow", "application/pdf", "unknown")); !errors.Is(e, ErrLimit) {
		t.Fatalf("original cap %v", e)
	}
	s.Cfg.AllowedURLs = []string{"https://pdf.test/a.pdf"}
	s.Cfg.MaxQueued = 1
	j, _, e := s.SubmitSource(ctx, "https://pdf.test/a.pdf", "test", r.CaptureID, "public")
	if e != nil {
		t.Fatal(e)
	}
	dup, existing, e := s.SubmitSource(ctx, "https://pdf.test/a.pdf", "test", r.CaptureID, "public")
	if e != nil || !existing || dup.JobID != j.JobID {
		t.Fatalf("job duplicate at cap %+v %v %v", dup, existing, e)
	}
}

func TestReportLookupRejectsForgedSchemaIdentity(t *testing.T) {
	s := reportService(t)
	m := manifestFixture()
	b := manifestJSON(t, m)
	r, e := s.A.ImportReport(ctx, archive.ReportRequest{Key: "wrong-key", Supplier: manifestSupplier, Title: manifestTitle, Content: strings.NewReader(b)})
	if e != nil {
		t.Fatal(e)
	}
	if _, e := s.ReportStatus(ctx, r.CaptureID); !errors.Is(e, archive.ErrInvalid) {
		t.Fatalf("wrong key accepted: %v", e)
	}
	r, e = s.A.ImportReport(ctx, archive.ReportRequest{Key: manifestKeyPrefix + m.ReportID, Supplier: manifestSupplier, Title: "SECRET report", Content: strings.NewReader(b)})
	if e != nil {
		t.Fatal(e)
	}
	if _, e := s.ReportStatus(ctx, r.CaptureID); !errors.Is(e, archive.ErrInvalid) {
		t.Fatalf("wrong title accepted: %v", e)
	}
}

func TestImportedReferenceStateAndRecordedTime(t *testing.T) {
	s := reportService(t)
	m := manifestFixture()
	req := originalFixture(pdfBody, "application/pdf", "unknown")
	m.Sources[1].ExternalOriginal.SHA256 = req.ExpectedSHA256
	m.Sources[1].ExternalOriginal.Size = req.ExpectedSize
	r, e := s.ImportManifest(ctx, manifestJSON(t, m))
	if e != nil {
		t.Fatal(e)
	}
	if _, e := s.ImportOriginal(ctx, r.CaptureID, "kci", req); e != nil {
		t.Fatal(e)
	}
	got, e := s.ReportStatus(ctx, r.CaptureID)
	if e != nil {
		t.Fatal(e)
	}
	src := got.Sources[1]
	if src.State != "stored" || src.ExternalOriginal.Preservation != "matching_copy_preserved_in_trial" || src.ExternalOriginal.Evidence != "reference_claim" || src.Captures[0].Completeness != "unknown" {
		t.Fatalf("state %+v", src)
	}
	if _, e := time.Parse(time.RFC3339Nano, src.Captures[0].RecordedAt); e != nil {
		t.Fatalf("recorded_at: %v", e)
	}
	if got.Aggregate.StoredSources != 1 || got.Aggregate.MissingSources != 1 || got.Aggregate.StoredCaptures != 1 {
		t.Fatalf("aggregate %+v", got.Aggregate)
	}
}

func TestReportStatusMapsOnlySafeFailureCodes(t *testing.T) {
	for _, tc := range []struct{ detail, want string }{
		{"rate_limited: HTTP 429; SECRET token=abc", "rate_limited"},
		{"access restricted (HTTP 403 or similar); SECRET", "access_restricted"},
		{"media_not_allowed: SECRET", "media_not_allowed"},
		{"trial policy: SECRET /private", "policy_refused"},
		{"network error: timed out", "network_timeout"},
		{"SECRET rate_limited", "collection_failed"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			s := reportService(t)
			r := importFixture(t, s)
			s.Cfg.AllowedURLs = []string{"https://pdf.test/a.pdf"}
			j, _, e := s.SubmitSource(ctx, "https://pdf.test/a.pdf", "test", r.CaptureID, "public")
			if e != nil {
				t.Fatal(e)
			}
			claimed, e := s.A.ClaimJob(ctx, archive.ExecutorHTTP, "owner", time.Minute)
			if e != nil || claimed == nil {
				t.Fatal(e)
			}
			if _, e := s.A.CompleteJob(ctx, j.JobID, "owner", claimed.AttemptsMade, archive.JobOutcome{State: archive.JobHuman, Detail: tc.detail}); e != nil {
				t.Fatal(e)
			}
			got, e := s.ReportStatus(ctx, r.CaptureID)
			if e != nil || got.Sources[0].FailureReason != tc.want {
				t.Fatalf("status %+v %v", got, e)
			}
			b, _ := json.Marshal(got)
			if bytes.Contains(b, []byte("SECRET")) {
				t.Fatalf("leak %s", b)
			}
		})
	}
}

func TestReportAndOriginalCapsAcrossHandles(t *testing.T) {
	s := reportService(t)
	second, e := Open(s.Cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer second.Close()
	var wg sync.WaitGroup
	results := make(chan error, MaxTrialReports+4)
	for i := 0; i < MaxTrialReports+4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			svc := s
			if i%2 == 1 {
				svc = second
			}
			m := manifestFixture()
			m.ReportID = "concurrent-" + strings.Repeat("x", i+1)
			_, err := svc.ImportManifest(ctx, manifestJSON(t, m))
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrLimit) {
			t.Fatal(err)
		}
	}
	if success != MaxTrialReports {
		t.Fatalf("report admissions=%d", success)
	}
	var reportRef string
	// One of the concurrently admitted references is known by reading the bounded
	// set; no report or source body is exposed by this status lookup.
	for i := 0; i < MaxTrialReports+4; i++ {
		ref := "concurrent-" + strings.Repeat("x", i+1)
		if _, err := s.ReportStatus(ctx, ref); err == nil {
			reportRef = ref
			break
		}
	}
	if reportRef == "" {
		t.Fatal("no report")
	}
	results = make(chan error, MaxSourceOriginals+4)
	for i := 0; i < MaxSourceOriginals+4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			svc := s
			if i%2 == 1 {
				svc = second
			}
			_, err := svc.ImportOriginal(ctx, reportRef, "kci", originalFixture(pdfBody+strings.Repeat("x", i+1), "application/pdf", "unknown"))
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	success = 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrLimit) {
			t.Fatal(err)
		}
	}
	if success != MaxSourceOriginals {
		t.Fatalf("original admissions=%d", success)
	}
}

func TestFetchedCaptureStatusKeepsRecoveredProvenance(t *testing.T) {
	s := reportService(t)
	r := importFixture(t, s)
	s.Cfg.AllowedURLs = []string{"https://pdf.test/a.pdf"}
	j, _, e := s.SubmitSource(ctx, "https://pdf.test/a.pdf", "test", r.CaptureID, "public")
	if e != nil {
		t.Fatal(e)
	}
	claimed, e := s.A.ClaimJob(ctx, archive.ExecutorHTTP, "owner", time.Minute)
	if e != nil || claimed == nil {
		t.Fatal(e)
	}
	c, e := s.A.CaptureSource(ctx, archive.SourceRequest{Content: strings.NewReader(pdfBody), MediaType: "application/pdf", Supplier: archive.JobSupplierPrefix + j.JobID, Title: "SECRET title", Completeness: "unknown"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e := s.A.CompleteJob(ctx, j.JobID, "owner", claimed.AttemptsMade, archive.JobOutcome{State: archive.JobHeld, CaptureID: c.CaptureID}); e != nil {
		t.Fatal(e)
	}
	got, e := s.ReportStatus(ctx, r.CaptureID)
	if e != nil {
		t.Fatal(e)
	}
	if got.Sources[0].State != "held" || len(got.Sources[0].Captures) != 1 || got.Sources[0].Captures[0].Provenance != archive.ProvRecovered || got.Sources[0].Captures[0].Completeness != "unknown" {
		t.Fatalf("status %+v", got)
	}
	b, _ := json.Marshal(got)
	if bytes.Contains(b, []byte("SECRET")) {
		t.Fatalf("leak %s", b)
	}
}
