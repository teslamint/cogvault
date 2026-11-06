package kcibrowser

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/teslamint/cogvault/internal/archive"
)

const (
	kciFixture = "https://www.kci.go.kr/kciportal/ci/sereArticleSearch/ciSereArtiView.kci?sereArticleSearchBean.artiId=ART002605798"
	// referencePDFSHA is the SHA-256 of an earlier official browser download of
	// the fixture article, recorded by the operator. A live run reports whether
	// today's download is byte-identical; it does not require it, because the
	// portal may regenerate the PDF.
	referencePDFSHA = "75989f5266d8b0f5a1e54b7ff248956aea988b5beb27548e823b697c405143c4"
)

// mapTo stands a local page in for the portal behind the runner's fixed-host
// URL check: the runner passes the normalized KCI URL, this wrapper forwards the
// fetch to the real browser driver on the local page.
type mapTo struct {
	d     *Driver
	local string
}

func (m mapTo) Fetch(ctx context.Context, _ string) (archive.BrowserFetch, error) {
	return m.d.Fetch(ctx, m.local)
}

// The whole chain on a real browser: a queued job, the runner's URL policy and
// claim, the browser click, the capture registry, and the job state. A page
// that offers no control ends human_action_required and stores nothing.
func TestQueueToCaptureThroughARealBrowser(t *testing.T) {
	haveChrome(t)
	_, u := portal(t)
	a, err := archive.Open(filepath.Join(t.TempDir(), "archive"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx := context.Background()

	ok, _, err := a.SubmitJob(ctx, archive.JobRequest{URL: kciFixture, Executor: archive.ExecutorBrowser, Submitter: "e2e"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := a.RunNextBrowserJob(ctx, mapTo{testDriver(), u("/ok")}, "w", archive.DefaultJobLease)
	if err != nil || got == nil || got.JobID != ok.JobID || got.State != archive.JobHeld || got.ResultCaptureID == "" {
		t.Fatalf("job = %+v, %v", got, err)
	}
	c, err := a.Status(ctx, got.ResultCaptureID)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(pdfBody))
	if c.ObjectID != "sha256:"+hex.EncodeToString(sum[:]) || c.MediaType != "application/pdf" || c.Completeness != "unknown" || c.Ingest.State != "held" || c.URL != kciFixture {
		t.Fatalf("capture = %+v", c)
	}
	if !strings.Contains(got.Attempts[0].Response, `"download_host":"127.0.0.1"`) {
		t.Fatalf("attempt evidence = %q", got.Attempts[0].Response)
	}

	none, _, err := a.SubmitJob(ctx, archive.JobRequest{URL: strings.Replace(kciFixture, "ART002605798", "ART000000009", 1), Executor: archive.ExecutorBrowser, Submitter: "e2e"})
	if err != nil {
		t.Fatal(err)
	}
	got, err = a.RunNextBrowserJob(ctx, mapTo{testDriver(), u("/nobutton")}, "w", archive.DefaultJobLease)
	if err != nil || got == nil || got.JobID != none.JobID || got.State != archive.JobHuman || got.ResultCaptureID != "" ||
		!strings.HasPrefix(got.Attempts[0].Detail, archive.ReasonFulltextNotPublic+": ") {
		t.Fatalf("no-control job = %+v, %v", got, err)
	}
}

// TestLiveKCI runs the real portal through the real browser. It is opt-in
// (COGVAULT_LIVE_KCI=1) because it needs a browser, network access to the
// portal, and an article whose society has made the full text public, and
// because it contacts a third-party site. It reports, and does not require,
// whether the download is byte-identical to the operator's earlier copy.
func TestLiveKCI(t *testing.T) {
	if os.Getenv("COGVAULT_LIVE_KCI") != "1" {
		t.Skip("set COGVAULT_LIVE_KCI=1 to contact the real KCI portal")
	}
	haveChrome(t)
	a, err := archive.Open(filepath.Join(t.TempDir(), "archive"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	d := New(Options{})

	t.Run("public article", func(t *testing.T) {
		j, _, err := a.SubmitJob(ctx, archive.JobRequest{URL: kciFixture, Executor: archive.ExecutorBrowser, Submitter: "live", SeenSHA256: "sha256:" + referencePDFSHA})
		if err != nil {
			t.Fatal(err)
		}
		got, err := a.RunNextBrowserJob(ctx, d, "live", archive.DefaultJobLease)
		if err != nil || got == nil || got.JobID != j.JobID {
			t.Fatalf("job = %+v, %v", got, err)
		}
		t.Logf("state=%s attempts=%+v", got.State, got.Attempts)
		if got.State != archive.JobHeld {
			t.Fatalf("the public article was not downloaded: state %s, detail %q", got.State, got.Attempts[len(got.Attempts)-1].Detail)
		}
		c, err := a.Status(ctx, got.ResultCaptureID)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("capture object=%s size=%d provenance=%s; reference sha256:%s identical=%v", c.ObjectID, c.Size, got.Provenance, referencePDFSHA, c.ObjectID == "sha256:"+referencePDFSHA)
		if c.MediaType != "application/pdf" || c.Size <= 0 || c.Completeness != "unknown" {
			t.Fatalf("capture = %+v", c)
		}
	})

	t.Run("failure: no such article", func(t *testing.T) {
		j, _, err := a.SubmitJob(ctx, archive.JobRequest{URL: strings.Replace(kciFixture, "ART002605798", "ART000000001", 1), Executor: archive.ExecutorBrowser, Submitter: "live"})
		if err != nil {
			t.Fatal(err)
		}
		got, err := a.RunNextBrowserJob(ctx, d, "live", archive.DefaultJobLease)
		if err != nil || got == nil || got.JobID != j.JobID {
			t.Fatalf("job = %+v, %v", got, err)
		}
		t.Logf("state=%s detail=%q", got.State, got.Attempts[len(got.Attempts)-1].Detail)
		if got.State == archive.JobHeld || got.ResultCaptureID != "" {
			t.Fatalf("a missing article stored bytes: %+v", got)
		}
	})
}
