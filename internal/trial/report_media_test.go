package trial

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/teslamint/cogvault/internal/archive"
)

func TestReportPDFOnlyGenericMIMEAcquisitionAndStatus(t *testing.T) {
	for _, media := range []string{"", "application/octet-stream"} {
		t.Run(media, func(t *testing.T) {
			s := reportService(t)
			r := importFixture(t, s)
			s.Cfg.AllowedURLs = []string{"https://pdf.test/a.pdf"}
			j, _, err := s.SubmitSource(ctx, "https://pdf.test/a.pdf", "test", r.CaptureID, "public")
			if err != nil {
				t.Fatal(err)
			}
			claimed, err := s.A.ClaimJob(ctx, archive.ExecutorHTTP, "test", time.Minute)
			if err != nil || claimed == nil {
				t.Fatal(err)
			}
			if err := s.validateReportJob(ctx, claimed); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header()["Content-Type"] = []string{media}
				_, _ = w.Write([]byte(pdfBody))
			}))
			defer server.Close()
			u, _ := url.Parse(server.URL)
			q := s.reportAcquirer()
			if !q.PDFOnly || len(q.AllowedMediaTypes) != 1 || q.AllowedMediaTypes[0] != "application/pdf" {
				t.Fatal("PDF-only report policy is not explicit")
			}
			// Preserve the production media policy and public-address check, using
			// a local HTTP transport fixture rather than any live source request.
			q.Resolve = func(context.Context, string) ([]netip.Addr, error) {
				return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
			}
			q.Dial = func(c context.Context, network, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(c, network, u.Host)
			}
			q.AllowURL = nil
			copy := *claimed
			copy.URL = "http://pdf.test/a.pdf"
			done, err := s.A.RunClaimedJob(ctx, q, &copy, "test")
			if err != nil || done == nil || done.State != archive.JobHeld {
				t.Fatalf("acquisition: %+v %v", done, err)
			}
			got, err := s.ReportStatus(ctx, r.CaptureID)
			if err != nil || len(got.Sources[0].Captures) != 1 || got.Sources[0].JobID != j.JobID || got.Aggregate.StoredCaptures != 1 {
				t.Fatalf("status: %+v %v", got, err)
			}
			if c := got.Sources[0].Captures[0]; c.MediaType != "application/pdf" || c.Completeness != "unknown" {
				t.Fatalf("PDF format lost: %+v", c)
			}
		})
	}
}

func TestReportStatusPreservesAcceptedXHTMLCapture(t *testing.T) {
	s := reportService(t)
	r := importFixture(t, s)
	s.Cfg.AllowedURLs = []string{"https://pdf.test/a.pdf"}
	j, _, err := s.SubmitSource(ctx, "https://pdf.test/a.pdf", "test", r.CaptureID, "public")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.A.ClaimJob(ctx, archive.ExecutorHTTP, "test", time.Minute)
	if err != nil || claimed == nil {
		t.Fatal(err)
	}
	c, err := s.A.CaptureSource(ctx, archive.SourceRequest{Content: strings.NewReader(`<html xmlns="http://www.w3.org/1999/xhtml"><body>Source</body></html>`), MediaType: "application/xhtml+xml", Supplier: archive.JobSupplierPrefix + j.JobID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.A.CompleteJob(ctx, j.JobID, "test", claimed.AttemptsMade, archive.JobOutcome{State: archive.JobHeld, CaptureID: c.CaptureID}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReportStatus(ctx, r.CaptureID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Sources[0].State != "held" || len(got.Sources[0].Captures) != 1 || got.Aggregate.StoredSources != 1 || got.Aggregate.StoredCaptures != 1 || got.Aggregate.MissingSources != 1 {
		t.Fatalf("accepted XHTML silently lost: %+v", got)
	}
	if cv := got.Sources[0].Captures[0]; cv.MediaType != "application/xhtml+xml" || cv.Completeness != "unknown" || cv.Provenance != archive.ProvRecovered {
		t.Fatalf("XHTML evidence changed: %+v", cv)
	}
}
