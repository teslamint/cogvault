package archive

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestReadReportBoundedVerified(t *testing.T) {
	a, _ := openTest(t)
	ctx := context.Background()
	r, err := a.ImportReport(ctx, ReportRequest{Key: "selected:r1", Content: strings.NewReader(`{"version":1}`), Supplier: "trial-selected", Title: "Selected sources"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{r.CaptureID, "selected:r1"} {
		got, err := a.ReadReport(ctx, ref, "trial-selected", 8192)
		if err != nil || string(got.Content) != `{"version":1}` {
			t.Fatalf("read=%+v %v", got, err)
		}
	}
	if _, err := a.ReadReport(ctx, r.CaptureID, "other", 8192); !errors.Is(err, ErrNotFound) {
		t.Fatalf("supplier %v", err)
	}
	if _, err := a.ReadReport(ctx, r.CaptureID, "trial-selected", 4); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("size %v", err)
	}
	if _, err := a.ReadReport(ctx, r.CaptureID, "trial-selected", 8193); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bound %v", err)
	}
	src, err := a.CaptureSource(ctx, source("not a report", "https://example.org/a"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.ReadReport(ctx, src.CaptureID, "", 8192); !errors.Is(err, ErrInvalid) {
		t.Fatalf("supplier required %v", err)
	}
	p := a.objectPath(strings.TrimPrefix(r.ObjectID, "sha256:"))
	if err := os.Chmod(p, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(`{"version":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ReadReport(ctx, r.CaptureID, "trial-selected", 8192); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("tamper %v", err)
	}
}

func TestReportReadMetadataBoundsAndFiltering(t *testing.T) {
	a, _ := openTest(t)
	ctx := context.Background()
	r, err := a.ImportReport(ctx, ReportRequest{Content: strings.NewReader("report"), Supplier: "test"})
	if err != nil {
		t.Fatal(err)
	}
	for _, claim := range []string{"source:a", "source:b"} {
		_, _, err := a.SubmitJob(ctx, JobRequest{URL: "https://example.org/test.pdf", Submitter: "secret submitter", ReportCaptureID: r.CaptureID, ClaimRef: claim})
		if err != nil {
			t.Fatal(err)
		}
	}
	jobs, err := a.GetReportJobs(ctx, r.CaptureID, 1)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs %v %v", jobs, err)
	}
	if jobs[0].Submitter != "" || len(jobs[0].Attempts) != 0 || jobs[0].LeaseOwner != "" {
		t.Fatalf("unsafe projection %+v", jobs[0])
	}
	if _, err := a.GetReportJobs(ctx, r.CaptureID, 101); !errors.Is(err, ErrInvalid) {
		t.Fatalf("limit %v", err)
	}
	s := source("%PDF-1.7\nbody", "https://example.org/a")
	s.Supplier = "trial originals"
	s.Title = "existing_file"
	r2, err := a.CaptureSource(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	caps, err := a.GetCapturesBySupplier(ctx, "trial originals", 10)
	if err != nil || len(caps) != 1 || caps[0].CaptureID != r2.CaptureID {
		t.Fatalf("captures %v %v", caps, err)
	}
	cap, err := a.CaptureMetadata(ctx, r2.CaptureID)
	if err != nil || cap.Supplier != s.Supplier {
		t.Fatalf("metadata %v %v", cap, err)
	}
}
