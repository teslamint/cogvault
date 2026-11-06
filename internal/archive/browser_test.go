package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

const kciFixtureURL = "https://www.kci.go.kr/kciportal/ci/sereArticleSearch/ciSereArtiView.kci?sereArticleSearchBean.artiId=ART002605798"

type fakeBrowser struct {
	fn    func(ctx context.Context, url string) (BrowserFetch, error)
	calls []string
}

func (f *fakeBrowser) Fetch(ctx context.Context, url string) (BrowserFetch, error) {
	f.calls = append(f.calls, url)
	return f.fn(ctx, url)
}

func respond(code string, body string) *fakeBrowser {
	return &fakeBrowser{fn: func(context.Context, string) (BrowserFetch, error) {
		return BrowserFetch{Code: code, Body: []byte(body), Evidence: `{"executor":"browser"}`}, nil
	}}
}

// Only the KCI article detail page of one article is a browser job. The query
// is admitted here and nowhere else: it is the public article ID on a fixed
// host and path.
func TestCheckBrowserURLAdmitsOnlyTheArticleDetailPage(t *testing.T) {
	got, id, err := CheckBrowserURL(kciFixtureURL)
	if err != nil || id != "ART002605798" || got != kciFixtureURL {
		t.Fatalf("fixture = %q, %q, %v", got, id, err)
	}
	if got, _, err := CheckBrowserURL("https://www.kci.go.kr:443/kciportal/ci/sereArticleSearch/ciSereArtiView.kci?sereArticleSearchBean.artiId=ART1"); err != nil || !strings.HasPrefix(got, "https://www.kci.go.kr/") {
		t.Fatalf("explicit port 443 = %q, %v", got, err)
	}
	const base = "https://www.kci.go.kr/kciportal/ci/sereArticleSearch/ciSereArtiView.kci"
	for name, raw := range map[string]string{
		"http":             "http://www.kci.go.kr/kciportal/ci/sereArticleSearch/ciSereArtiView.kci?sereArticleSearchBean.artiId=ART1",
		"other host":       "https://www.kci.go.kr.example.org/kciportal/ci/sereArticleSearch/ciSereArtiView.kci?sereArticleSearchBean.artiId=ART1",
		"userinfo":         "https://u:p@www.kci.go.kr/kciportal/ci/sereArticleSearch/ciSereArtiView.kci?sereArticleSearchBean.artiId=ART1",
		"other port":       "https://www.kci.go.kr:8443/kciportal/ci/sereArticleSearch/ciSereArtiView.kci?sereArticleSearchBean.artiId=ART1",
		"other path":       "https://www.kci.go.kr/kciportal/ci/sereArticleSearch/ciSereArtiOrteServHistIFrame.kci?sereArticleSearchBean.artiId=ART1",
		"no query":         base,
		"bare ?":           base + "?",
		"extra param":      base + "?sereArticleSearchBean.artiId=ART1&token=x",
		"other param":      base + "?id=ART1",
		"duplicate param":  base + "?sereArticleSearchBean.artiId=ART1&sereArticleSearchBean.artiId=ART2",
		"lowercase id":     base + "?sereArticleSearchBean.artiId=art1",
		"non-numeric id":   base + "?sereArticleSearchBean.artiId=ART12x",
		"too long id":      base + "?sereArticleSearchBean.artiId=ART1234567890123",
		"fragment":         base + "?sereArticleSearchBean.artiId=ART1#p3",
		"uppercase host":   "https://WWW.KCI.GO.KR/kciportal/ci/sereArticleSearch/ciSereArtiView.kci?sereArticleSearchBean.artiId=ART1",
		"an internal host": "https://10.0.0.5/kciportal/ci/sereArticleSearch/ciSereArtiView.kci?sereArticleSearchBean.artiId=ART1",
		"path with dotdot": "https://www.kci.go.kr/kciportal/ci/../ci/sereArticleSearch/ciSereArtiView.kci?sereArticleSearchBean.artiId=ART1",
	} {
		if _, _, err := CheckBrowserURL(raw); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: CheckBrowserURL(%q) = %v, want ErrInvalid", name, raw, err)
		}
	}
}

// The browser executor takes the KCI query URL; the HTTP executor still
// refuses every query, and a browser job cannot name any other site.
func TestSubmitJobAppliesTheExecutorsURLPolicy(t *testing.T) {
	a, _ := openTest(t)
	j := submit(t, a, JobRequest{URL: kciFixtureURL, Executor: ExecutorBrowser})
	if j.URL != kciFixtureURL || j.Executor != ExecutorBrowser {
		t.Fatalf("browser job = %+v", j)
	}
	if _, _, err := a.SubmitJob(ctx, JobRequest{URL: kciFixtureURL, Submitter: "t"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("HTTP executor accepted the KCI query URL: %v", err)
	}
	if _, _, err := a.SubmitJob(ctx, JobRequest{URL: "https://example.org/a.pdf", Submitter: "t", Executor: ExecutorBrowser}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("browser job accepted another site: %v", err)
	}
}

// A downloaded PDF is stored through the capture registry as a held capture of
// unknown completeness, with the browser acquisition method on its download
// record, and the job says whether the bytes match what the research read.
func TestRunNextBrowserJobStoresHeldCaptureWithProvenance(t *testing.T) {
	a, _ := openTest(t)
	pdf := "%PDF-1.4\nbody\n%%EOF\n"
	sum := sha256.Sum256([]byte(pdf))
	seen := "sha256:" + hex.EncodeToString(sum[:])
	plain := submit(t, a, JobRequest{URL: kciFixtureURL, Executor: ExecutorBrowser})
	d := respond("", pdf)
	got, err := a.RunNextBrowserJob(ctx, d, "b1", DefaultJobLease)
	if err != nil || got == nil || got.JobID != plain.JobID || got.State != JobHeld || got.Provenance != ProvRecovered || got.ResultCaptureID == "" {
		t.Fatalf("job = %+v, %v", got, err)
	}
	if len(d.calls) != 1 || d.calls[0] != kciFixtureURL {
		t.Fatalf("driver calls = %v", d.calls)
	}
	c := mustStatus(t, a, got.ResultCaptureID)
	if c.MediaType != "application/pdf" || c.OriginalName != "kci-ART002605798.pdf" || c.URL != kciFixtureURL || c.Completeness != "unknown" ||
		c.Ingest.State != "held" || !strings.HasPrefix(c.Supplier, JobSupplierPrefix+got.JobID) {
		t.Fatalf("capture = %+v", c)
	}
	if c.Download == nil || !strings.Contains(c.Download.Headers["Acquisition-Method"], "browser_download") || c.Download.Size != int64(len(pdf)) {
		t.Fatalf("download record = %+v", c.Download)
	}
	if len(c.Collections) != 1 || c.Collections[0].Provenance != ProvRecovered {
		t.Fatalf("collections = %+v", c.Collections)
	}

	// The same bytes under a job that carries the digest the research read.
	other := strings.Replace(kciFixtureURL, "ART002605798", "ART000000001", 1)
	sj := submit(t, a, JobRequest{URL: other, Executor: ExecutorBrowser, SeenSHA256: seen})
	got, err = a.RunNextBrowserJob(ctx, d, "b1", DefaultJobLease)
	if err != nil || got == nil || got.JobID != sj.JobID || got.Provenance != ProvSeen {
		t.Fatalf("seen job = %+v, %v", got, err)
	}
	if none, err := a.RunNextBrowserJob(ctx, d, "b1", DefaultJobLease); err != nil || none != nil {
		t.Fatalf("empty queue = %+v, %v", none, err)
	}
}

// Every case where the portal does not hand over a file ends with a fixed
// code, stores nothing, and a person-needed case is human_action_required,
// never a retry loop.
func TestRunNextBrowserJobOutcomeCodes(t *testing.T) {
	for _, tc := range []struct {
		code, state string
	}{
		{ReasonFulltextNotPublic, JobHuman},
		{ReasonLoginRequired, JobHuman},
		{ReasonConsentRequired, JobHuman},
		{ReasonChallenge, JobHuman},
		{ReasonExternalSite, JobHuman},
		{ReasonDownloadNotStarted, JobHuman},
		{ReasonUnexpectedPage, JobHuman},
		{ReasonTooLarge, JobFailed},
		{ReasonBlockedAddress, JobFailed},
		{"made_up_by_a_buggy_driver", JobFailed},
	} {
		t.Run(tc.code, func(t *testing.T) {
			a, _ := openTest(t)
			submit(t, a, JobRequest{URL: kciFixtureURL, Executor: ExecutorBrowser})
			got, err := a.RunNextBrowserJob(ctx, respond(tc.code, ""), "b1", DefaultJobLease)
			if err != nil || got == nil || got.State != tc.state || got.ResultCaptureID != "" || len(got.Attempts) != 1 {
				t.Fatalf("job = %+v, %v", got, err)
			}
			if tc.state == JobHuman && got.Attempts[0].Outcome != JobHuman {
				t.Fatalf("attempt = %+v", got.Attempts[0])
			}
			var objects int
			if err := a.db.QueryRow(`SELECT COUNT(*) FROM objects`).Scan(&objects); err != nil || objects != 0 {
				t.Fatalf("objects = %d, %v; a refused case must store nothing", objects, err)
			}
			if tc.state == JobHuman && !strings.HasPrefix(got.Attempts[0].Detail, tc.code+": ") {
				t.Fatalf("detail = %q", got.Attempts[0].Detail)
			}
		})
	}
}

// A file that is not a PDF is never stored as the article, and a driver fault
// is retried with a reason that names no address.
func TestRunNextBrowserJobRejectsNonPDFAndRetriesFaults(t *testing.T) {
	a, _ := openTest(t)
	submit(t, a, JobRequest{URL: kciFixtureURL, Executor: ExecutorBrowser})
	got, err := a.RunNextBrowserJob(ctx, respond("", "<html>not a pdf</html>"), "b1", DefaultJobLease)
	if err != nil || got == nil || got.State != JobFailed || got.ResultCaptureID != "" || !strings.HasPrefix(got.Attempts[0].Detail, ReasonNotPDF) {
		t.Fatalf("non-PDF = %+v, %v", got, err)
	}
	big := &fakeBrowser{fn: func(context.Context, string) (BrowserFetch, error) {
		return BrowserFetch{Body: append([]byte("%PDF-"), bytes.Repeat([]byte("x"), MaxObjectBytes)...)}, nil
	}}
	b := strings.Replace(kciFixtureURL, "ART002605798", "ART000000002", 1)
	submit(t, a, JobRequest{URL: b, Executor: ExecutorBrowser})
	if got, err = a.RunNextBrowserJob(ctx, big, "b1", DefaultJobLease); err != nil || got.State != JobFailed || !strings.HasPrefix(got.Attempts[0].Detail, ReasonTooLarge) {
		t.Fatalf("oversize = %+v, %v", got, err)
	}

	clk := withClock(a)
	c := strings.Replace(kciFixtureURL, "ART002605798", "ART000000003", 1)
	submit(t, a, JobRequest{URL: c, Executor: ExecutorBrowser})
	clk.t = clk.t.Add(time.Hour)
	fault := &fakeBrowser{fn: func(context.Context, string) (BrowserFetch, error) {
		return BrowserFetch{}, fmt.Errorf("websocket 127.0.0.1:9222 dial tcp 10.1.2.3:443: connection refused")
	}}
	got, err = a.RunNextBrowserJob(ctx, fault, "b1", DefaultJobLease)
	if err != nil || got == nil || got.State != "queued" || strings.Contains(got.Attempts[0].Detail, "10.1.2.3") || strings.Contains(got.Attempts[0].Detail, "9222") {
		t.Fatalf("fault = %+v, %v", got, err)
	}
}

// A run cut off by its context records nothing: the lease expires and the job
// resumes. The browser runner never claims an HTTP job and the HTTP worker
// never claims a browser job.
func TestRunNextBrowserJobContextAndExecutorIsolation(t *testing.T) {
	a, _ := openTest(t)
	httpJob := submit(t, a, JobRequest{URL: "http://public.test/a.pdf"})
	if got, err := a.RunNextBrowserJob(ctx, respond("", "%PDF-1"), "b1", DefaultJobLease); err != nil || got != nil {
		t.Fatalf("browser runner claimed an HTTP job: %+v, %v", got, err)
	}
	if j := getJob(t, a, httpJob.JobID); j.State != "queued" {
		t.Fatalf("HTTP job = %+v", j)
	}
	bj := submit(t, a, JobRequest{URL: kciFixtureURL, Executor: ExecutorBrowser})
	cctx, cancel := context.WithCancel(ctx)
	blocked := &fakeBrowser{fn: func(c context.Context, _ string) (BrowserFetch, error) {
		cancel()
		<-c.Done()
		return BrowserFetch{}, c.Err()
	}}
	if _, err := a.RunNextBrowserJob(cctx, blocked, "b1", time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled run = %v", err)
	}
	if j := getJob(t, a, bj.JobID); j.State != "running" || len(j.Attempts) != 0 {
		t.Fatalf("a cancelled run recorded a result: %+v", j)
	}
}
