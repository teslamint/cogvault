package archive

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTrialMediaPolicyValidatesBodyBeforeStorage(t *testing.T) {
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	_, _ = zw.Write([]byte("%PDF-1.7 compressed"))
	_ = zw.Close()
	for _, tc := range []struct {
		name, media, encoding, body string
		allowed                     []string
		wantErr                     error
	}{
		{name: "pdf", media: "application/pdf", body: "%PDF-1.7 original"},
		{name: "pdf without declared type", body: "%PDF-1.7 original"},
		{name: "pdf octet stream", media: "application/octet-stream", body: "%PDF-1.7 original"},
		{name: "html", media: "text/html; charset=utf-8", body: "<!DOCTYPE html><html><title>Research</title><p>Original</p></html>"},
		{name: "html without type", body: "<html>Original</html>", wantErr: ErrMediaNotAllowed},
		{name: "html octet stream", media: "application/octet-stream", body: "<html>Original</html>", wantErr: ErrMediaNotAllowed},
		{name: "xhtml", media: "application/xhtml+xml", body: `<?xml version="1.0" encoding="UTF-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>Research</title></head><body>Original</body></html>`},
		{name: "html masquerading as pdf", media: "application/pdf", body: "<html><title>Research</title></html>", wantErr: ErrMediaNotAllowed},
		{name: "pdf masquerading as html", media: "text/html", body: "%PDF-1.7 original", wantErr: ErrMediaNotAllowed},
		{name: "plain text", media: "text/plain", body: "not an original PDF or HTML", wantErr: ErrMediaNotAllowed},
		{name: "json", media: "application/json", body: `{"message":"no"}`, wantErr: ErrMediaNotAllowed},
		{name: "json claiming html", media: "text/html", body: `{"message":"no"}`, wantErr: ErrMediaNotAllowed},
		{name: "binary claiming html", media: "text/html", body: "<html>\x00\xff</html>", wantErr: ErrMediaNotAllowed},
		{name: "html disallowed", media: "text/html", body: "<html>Original</html>", allowed: []string{"application/pdf"}, wantErr: ErrMediaNotAllowed},
		{name: "pdf disallowed", media: "application/pdf", body: "%PDF-1.7 original", allowed: []string{"text/html"}, wantErr: ErrMediaNotAllowed},
		{name: "gzip", media: "application/pdf", encoding: "gzip", body: compressed.String(), wantErr: ErrMediaNotAllowed},
		{name: "encoded despite pdf header", media: "application/pdf", encoding: "x-custom", body: "%PDF-1.7 original", wantErr: ErrMediaNotAllowed},
		{name: "undeclared gzip", media: "application/pdf", body: compressed.String(), wantErr: ErrMediaNotAllowed},
		{name: "non utf8 charset", media: "text/html; charset=utf-16", body: "<html>Original</html>", wantErr: ErrMediaNotAllowed},
		{name: "malformed media", media: "text/html; broken", body: "<html>Original</html>", wantErr: ErrMediaNotAllowed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, root := openTest(t)
			_, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.media)
				w.Header().Set("Content-Encoding", tc.encoding)
				fmt.Fprint(w, tc.body)
			}))
			q.AllowedMediaTypes = tc.allowed
			if q.AllowedMediaTypes == nil {
				q.AllowedMediaTypes = []string{"application/pdf", "text/html", "application/xhtml+xml"}
			}
			res, rec, err := a.Acquire(ctx, q, "http://public.test/source")
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error = %v, want %v", err, tc.wantErr)
				}
				if countCaptures(t, a) != 0 || len(objectFiles(t, root)) != 0 {
					t.Fatal("rejected body was stored")
				}
				if out := classifyAcquire(res, rec, err); out.State != JobFailed {
					t.Fatalf("outcome = %+v", out)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			c := mustStatus(t, a, res.CaptureID)
			if c.Completeness != "unknown" || rec.Size != int64(len(tc.body)) {
				t.Fatalf("capture = %+v, record = %+v", c, rec)
			}
			if strings.HasPrefix(tc.body, "%PDF-") && (c.MediaType != "application/pdf" || c.OriginalName != "source.pdf") {
				t.Fatalf("PDF with generic MIME lost its actual media type: %+v", c)
			}
			stored, err := os.ReadFile(filepath.Join(root, "objects", "sha256", objectFiles(t, root)[0]))
			if err != nil || string(stored) != tc.body {
				t.Fatalf("original bytes changed: %q, %v", stored, err)
			}
			if out := classifyAcquire(res, rec, nil); out.State != JobHeld {
				t.Fatalf("outcome = %+v", out)
			}
		})
	}
}

func TestTrialNilPolicyPreservesLegacyAcquirerBehavior(t *testing.T) {
	for _, pdfOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("pdfOnly%v", pdfOnly), func(t *testing.T) {
			a, _ := openTest(t)
			body := "arbitrary legacy bytes"
			if pdfOnly {
				body = "%PDF-1.7 legacy header-only check"
			}
			_, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/octet-stream")
				w.Header().Set("Content-Encoding", "x-custom")
				fmt.Fprint(w, body)
			}))
			q.PDFOnly = pdfOnly
			res, _, err := a.Acquire(ctx, q, "http://public.test/source")
			if err != nil {
				t.Fatal(err)
			}
			if c := mustStatus(t, a, res.CaptureID); c.OriginalName != "source.bin" || c.Completeness != "unknown" {
				t.Fatalf("legacy acquisition changed: %+v", c)
			}
		})
	}
	var hits atomic.Int32
	_, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, "/loop", http.StatusFound)
	}))
	if _, _, err := q.fetch(ctx, "http://public.test/start"); !errors.Is(err, ErrRemote) || hits.Load() != 6 {
		t.Fatalf("legacy redirect cap changed: requests = %d, error = %v", hits.Load(), err)
	}
}

func TestTrialHTMLAccessPagesStopWithoutStorageOrRetry(t *testing.T) {
	for name, body := range map[string]string{
		"login":   `<html><head><title>Sign in</title></head><body><form><input type="password"></form></body></html>`,
		"captcha": `<html><head><title>Verify you are human</title></head><body>Complete the CAPTCHA</body></html>`,
		"bot":     `<html><head><title>Just a moment...</title></head><body>Checking your browser before accessing the site</body></html>`,
		"access":  `<html><head><title>Access denied</title></head><body>Request ID: PRIVATE-TOKEN</body></html>`,
	} {
		t.Run(name, func(t *testing.T) {
			a, root := openTest(t)
			clk := withClock(a)
			var hits atomic.Int32
			_, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, body)
			}))
			q.AllowedMediaTypes = []string{"text/html"}
			submit(t, a, JobRequest{URL: "http://public.test/source", MaxAttempts: 3})
			job, err := a.RunNextJob(ctx, q, "worker", DefaultJobLease)
			if err != nil || job == nil || job.State != JobHuman || job.ResultCaptureID != "" || job.AttemptsMade != 1 {
				t.Fatalf("job = %+v, error = %v", job, err)
			}
			if strings.Contains(job.Attempts[0].Detail, "PRIVATE-TOKEN") {
				t.Fatal("response leaked into status")
			}
			clk.t = clk.t.Add(24 * time.Hour)
			if again, err := a.RunNextJob(ctx, q, "worker", DefaultJobLease); err != nil || again != nil {
				t.Fatalf("retried = %+v, %v", again, err)
			}
			if hits.Load() != 1 || countCaptures(t, a) != 0 || len(objectFiles(t, root)) != 0 {
				t.Fatal("access page was retried or stored")
			}
		})
	}
}

func TestTrialHTMLDoesNotFetchLinkedResources(t *testing.T) {
	a, _ := openTest(t)
	var hits atomic.Int32
	_, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>Research about CAPTCHA</title><meta http-equiv="refresh" content="0; url=/next"><script src="/code.js"></script></head><body><h1>Authentication research</h1><p>This research describes access denied messages.</p><a href="/paper.pdf">Paper</a><img src="/figure.png"><iframe src="/frame"></iframe></body></html>`)
	}))
	q.AllowedMediaTypes = []string{"text/html"}
	if _, _, err := a.Acquire(ctx, q, "http://public.test/source"); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("requests = %d, want 1", hits.Load())
	}
}

func TestTrialRedirectBudgetBoundsActualRequests(t *testing.T) {
	for _, max := range []int{0, 1, 5} {
		for _, terminal := range []bool{false, true} {
			t.Run(fmt.Sprintf("max%d/terminal%v", max, terminal), func(t *testing.T) {
				var hits atomic.Int32
				_, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					n := int(hits.Add(1))
					if terminal && n == max+1 {
						fmt.Fprint(w, "%PDF-1.7 original")
						return
					}
					http.Redirect(w, r, fmt.Sprintf("/hop%d", n), http.StatusFound)
				}))
				q.MaxRedirects = &max
				_, rec, err := q.fetch(ctx, "http://public.test/start")
				if terminal && err != nil {
					t.Fatal(err)
				}
				if !terminal && !errors.Is(err, ErrRemote) {
					t.Fatalf("error = %v, want redirect refusal", err)
				}
				if hits.Load() != int32(max+1) || len(rec.Hops) != max+1 {
					t.Fatalf("requests = %d, hops = %d, want %d", hits.Load(), len(rec.Hops), max+1)
				}
			})
		}
	}
}

func TestTrialInvalidPolicyRefusedBeforeRequest(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*Acquirer)
	}{
		{"negative redirects", func(q *Acquirer) { n := -1; q.MaxRedirects = &n }},
		{"excess redirects", func(q *Acquirer) { n := 6; q.MaxRedirects = &n }},
		{"unsupported type", func(q *Acquirer) { q.AllowedMediaTypes = []string{"image/png"} }},
		{"empty types", func(q *Acquirer) { q.AllowedMediaTypes = []string{} }},
		{"parameterized type", func(q *Acquirer) { q.AllowedMediaTypes = []string{"text/html; charset=utf-8"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("sent a request with invalid policy") }))
			tc.setup(&q)
			if _, _, err := q.fetch(ctx, "http://public.test/source"); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want ErrInvalid", err)
			}
			if len(f.dialed) != 0 || len(f.lookups) != 0 {
				t.Fatal("invalid policy caused network activity")
			}
		})
	}
}

func TestTrialRateLimitStopsOnlyWhenRequested(t *testing.T) {
	for _, stop := range []bool{false, true} {
		t.Run(fmt.Sprintf("stop%v", stop), func(t *testing.T) {
			a, root := openTest(t)
			clk := withClock(a)
			var hits atomic.Int32
			_, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Header().Set("Retry-After", "1")
				http.Error(w, "PRIVATE-TOKEN", http.StatusTooManyRequests)
			}))
			q.StopOnRateLimit = stop
			submit(t, a, JobRequest{URL: "http://public.test/source", MaxAttempts: 3})
			job, err := a.RunNextJob(ctx, q, "worker", DefaultJobLease)
			if err != nil || job == nil || job.AttemptsMade != 1 {
				t.Fatalf("job = %+v, %v", job, err)
			}
			if strings.Contains(job.Attempts[0].Detail, "PRIVATE-TOKEN") {
				t.Fatal("response leaked into detail")
			}
			if stop {
				if job.State != JobHuman || !strings.HasPrefix(job.Attempts[0].Detail, "rate_limited:") {
					t.Fatalf("job = %+v", job)
				}
			} else if job.State != "queued" {
				t.Fatalf("legacy 429 did not queue retry: %+v", job)
			}
			clk.t = clk.t.Add(24 * time.Hour)
			again, err := a.RunNextJob(ctx, q, "worker", DefaultJobLease)
			if err != nil {
				t.Fatal(err)
			}
			if stop && (again != nil || hits.Load() != 1) {
				t.Fatalf("429 retried: %+v, requests = %d", again, hits.Load())
			}
			if !stop && (again == nil || again.AttemptsMade != 2 || hits.Load() != 2) {
				t.Fatalf("legacy retry = %+v, requests = %d", again, hits.Load())
			}
			if countCaptures(t, a) != 0 || len(objectFiles(t, root)) != 0 {
				t.Fatal("429 body was stored")
			}
		})
	}
	if out := classifyAcquire(Result{}, AcquisitionRecord{}, ErrRateLimited); out.State != JobHuman {
		t.Fatalf("rate-limit outcome = %+v", out)
	}
}

func TestTrialPolicyKeepsRedirectURLChecks(t *testing.T) {
	for _, tc := range []struct {
		name, target string
		want         error
	}{
		{"query", "http://public.test/next?secret=PRIVATE-TOKEN", ErrInvalid},
		{"host", "http://other.test/next", ErrHostNotAllowed},
		{"scheme", "https://public.test/next", ErrURLNotAllowed},
		{"private", "http://private.test/next", ErrBlockedAddress},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			f, q := newFakeNet(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				http.Redirect(w, r, tc.target, http.StatusFound)
			}))
			f.hosts["private.test"] = []string{"10.0.0.1"}
			n := 1
			q.MaxRedirects = &n
			q.AllowedMediaTypes = []string{"application/pdf", "text/html"}
			q.AllowHost = func(h string) bool { return h == "public.test" || h == "private.test" }
			q.AllowURL = func(u *url.URL) bool { return u.Scheme == "http" }
			if _, _, err := q.fetch(ctx, "http://public.test/start"); !errors.Is(err, tc.want) || strings.Contains(err.Error(), "PRIVATE-TOKEN") {
				t.Fatalf("error = %v, want %v without secret", err, tc.want)
			}
			if hits.Load() != 1 || len(f.dialed) != 1 {
				t.Fatal("redirect contacted a refused destination")
			}
		})
	}
}
