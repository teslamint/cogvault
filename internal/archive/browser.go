package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"time"
)

// The browser executor's contract (ADR 0027, "KCI official-browser
// executor"). A job with executor "browser" names one KCI article detail page.
// A driver opens that page in a real browser, clicks the portal's own
// "KCI 원문 내려받기" control, and returns the file the portal serves, or a
// reason code when the portal does not offer it. This file holds everything
// that does not need a browser: the URL policy, the outcome codes, and
// RunNextBrowserJob, which turns a driver result into a stored capture or a
// job state. The driver (internal/kcibrowser) is behind BrowserDriver, so the
// whole contract is tested without a browser.
//
// What it never does: log in, reuse a person's session, solve a challenge, or
// choose a different download route. Free full text exists only where the
// society consented (KCI FAQ, as cited in ADR 0027); every other case ends as
// human_action_required with a code and no bytes.

const (
	// BrowserHost is the only host a browser job may name.
	BrowserHost       = "www.kci.go.kr"
	browserDetailPath = "/kciportal/ci/sereArticleSearch/ciSereArtiView.kci"
	browserArtiParam  = "sereArticleSearchBean.artiId"
	// KCIControlLabel is the visible label of the portal's official download
	// control on the article detail page.
	KCIControlLabel = "KCI 원문 내려받기"
)

// Outcome codes a driver reports in BrowserFetch.Code. They are fixed strings
// and carry no host, address, or page text.
const (
	ReasonFulltextNotPublic  = "fulltext_not_public"  // no official download control on the page
	ReasonLoginRequired      = "login_required"       // the click led to a login
	ReasonConsentRequired    = "consent_required"     // the click led to a consent step
	ReasonChallenge          = "challenge"            // the click led to a bot check
	ReasonExternalSite       = "external_site"        // the click led off the portal
	ReasonDownloadNotStarted = "download_not_started" // control present, no file within the bound
	ReasonTooLarge           = "too_large"            // the file exceeds the size bound
	ReasonBlockedAddress     = "blocked_address"      // the portal host resolved to a non-public address
	ReasonUnexpectedPage     = "unexpected_page"      // the page is not the article detail page
	ReasonNotPDF             = "not_a_pdf"            // the downloaded file has no PDF header
	reasonUnknown            = "unknown_browser_outcome"
)

// browserReasons maps each outcome code to the job state it ends in and the
// reason stored with the attempt. Remote clients read these strings.
var browserReasons = map[string]struct{ state, detail string }{
	ReasonFulltextNotPublic:  {JobHuman, "the article page has no official full-text download (the society has not made it public); only a preview or another site's copy may exist, so a person must decide"},
	ReasonLoginRequired:      {JobHuman, "the portal asked for a login; a person must obtain an authorized copy, the collector never logs in"},
	ReasonConsentRequired:    {JobHuman, "the portal asked for a consent step; a person must decide, the collector does not accept terms"},
	ReasonChallenge:          {JobHuman, "the portal showed a bot check; a person must act, the collector never solves it"},
	ReasonExternalSite:       {JobHuman, "the download control led away from the portal; a person must decide, the collector does not follow it"},
	ReasonDownloadNotStarted: {JobHuman, "the official download control was clicked but no file arrived; a person must check the page"},
	ReasonUnexpectedPage:     {JobHuman, "the page was not the article detail page; a person must check the URL"},
	ReasonTooLarge:           {JobFailed, fmt.Sprintf("the download is larger than the %d byte limit", MaxObjectBytes)},
	ReasonBlockedAddress:     {JobFailed, "destination is not a public address"},
	ReasonNotPDF:             {JobFailed, "the downloaded file is not a PDF"},
}

var artiIDPattern = regexp.MustCompile(`^ART[0-9]{1,12}$`)

// CheckBrowserURL admits only the KCI article detail page of one article:
// https, host www.kci.go.kr, the fixed detail path, no userinfo or fragment,
// and exactly one query parameter, the article ID (ART plus digits). That is
// the one narrow place a query is allowed, because the parameter is a public
// identifier and the host and path are fixed; the generic acquirer still
// refuses every query. It returns the normalized URL and the article ID.
func CheckBrowserURL(raw string) (normalized, artiID string, err error) {
	u, perr := url.Parse(raw)
	if perr != nil {
		return "", "", fmt.Errorf("%w: unparsable URL", ErrInvalid)
	}
	bad := func(why string) (string, string, error) {
		return "", "", fmt.Errorf("%w: browser jobs take only a KCI article detail page (%s)", ErrInvalid, why)
	}
	if u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.RawFragment != "" {
		return bad("https, no credentials, no fragment")
	}
	if u.Hostname() != BrowserHost || (u.Port() != "" && u.Port() != "443") {
		return bad("host " + BrowserHost)
	}
	if u.Path != browserDetailPath || u.ForceQuery {
		return bad("the article detail path")
	}
	q, qerr := url.ParseQuery(u.RawQuery)
	vals := q[browserArtiParam]
	if qerr != nil || len(q) != 1 || len(vals) != 1 || !artiIDPattern.MatchString(vals[0]) {
		return bad("exactly one article ID parameter")
	}
	return "https://" + BrowserHost + browserDetailPath + "?" + browserArtiParam + "=" + vals[0], vals[0], nil
}

// BrowserFetch is a driver's result for one article page. Code is empty when
// a file was downloaded; otherwise it is one of the Reason* codes and Body is
// empty. Evidence is operator-facing provenance (JSON), kept with the attempt
// and never shown to remote clients: it must not contain a resolver or socket
// address.
type BrowserFetch struct {
	Code     string
	Body     []byte
	Evidence string
}

// BrowserDriver performs the portal interaction. It returns an error only for
// a browser or network fault (a retry may succeed); contract outcomes, such as
// a page without the control, are BrowserFetch codes, never errors.
type BrowserDriver interface {
	Fetch(ctx context.Context, detailURL string) (BrowserFetch, error)
}

// RunNextBrowserJob claims one due browser job, runs the driver, and records
// the result. It returns nil when no job is due. As with RunNextJob, when ctx
// ends mid-run nothing is recorded: the lease expires and the job resumes.
func (a *Archive) RunNextBrowserJob(ctx context.Context, d BrowserDriver, owner string, lease time.Duration) (*Job, error) {
	job, err := a.ClaimJob(ctx, ExecutorBrowser, owner, lease)
	if err != nil || job == nil {
		return nil, err
	}
	var out JobOutcome
	if normalized, artiID, perr := CheckBrowserURL(job.URL); perr != nil {
		out = JobOutcome{State: JobFailed, Detail: "the job URL is not a KCI article detail page"}
	} else {
		f, ferr := d.Fetch(ctx, normalized)
		if ctx.Err() != nil {
			return job, ctx.Err()
		}
		// The capture and the job's completion are one transaction behind the
		// lease check: a try whose lease was reclaimed while the browser ran
		// stores nothing and returns ErrLeaseLost.
		bind := &jobBinding{id: job.JobID, owner: owner, attempt: job.AttemptsMade}
		out, err = a.browserOutcome(ctx, job, bind, normalized, artiID, f, ferr)
		if err != nil {
			return job, err
		}
		if bind.done != nil {
			return bind.done, nil
		}
	}
	done, err := a.CompleteJob(ctx, job.JobID, owner, job.AttemptsMade, out)
	if err != nil {
		return job, err
	}
	return &done, nil
}

// browserOutcome maps a driver result to a job outcome, storing the file
// through the same capture registry as every other capture. The capture is
// held: a download does not show the document is complete, so completeness
// stays unknown and release stays `reassess`. A stored file completes the job
// inside bind (bind.done is set and the returned outcome is unused); the error
// is ErrLeaseLost when the lease was reclaimed first.
func (a *Archive) browserOutcome(ctx context.Context, job *Job, bind *jobBinding, normalized, artiID string, f BrowserFetch, ferr error) (JobOutcome, error) {
	out := JobOutcome{Response: f.Evidence}
	switch {
	case ferr != nil:
		out.State, out.Detail = JobRetry, networkReason(ferr)
		if !errors.Is(ferr, context.DeadlineExceeded) {
			out.Detail = "browser error: the browser could not complete the request"
		}
		return out, nil
	case f.Code != "":
		r, ok := browserReasons[f.Code]
		if !ok {
			r.state, r.detail = JobFailed, "the browser executor returned an unrecognized outcome"
			f.Code = reasonUnknown
		}
		out.State, out.Detail = r.state, f.Code+": "+r.detail
		return out, nil
	}
	if int64(len(f.Body)) > MaxObjectBytes {
		r := browserReasons[ReasonTooLarge]
		out.State, out.Detail = r.state, ReasonTooLarge+": "+r.detail
		return out, nil
	}
	if !bytes.HasPrefix(f.Body, []byte("%PDF-")) {
		r := browserReasons[ReasonNotPDF]
		out.State, out.Detail = r.state, ReasonNotPDF+": "+r.detail
		return out, nil
	}
	fetchedAt := a.now().UTC().Format(time.RFC3339Nano)
	download, _ := json.Marshal(AcquisitionRecord{
		RequestedURL: normalized, FinalURL: normalized, Hops: []Hop{}, FetchedAt: fetchedAt, Size: int64(len(f.Body)),
		Headers: map[string]string{"Acquisition-Method": "browser_download (official control: " + KCIControlLabel + ")", "Content-Type": "application/pdf"},
	})
	bind.outcome = func(res Result, _ AcquisitionRecord) JobOutcome {
		return JobOutcome{
			State: JobHeld, CaptureID: res.CaptureID, Response: f.Evidence,
			Detail: "stored from the official download control; completeness unknown, so the capture is held from hand-off",
		}
	}
	_, err := a.captureSource(ctx, SourceRequest{
		Content: bytes.NewReader(f.Body), OriginalName: "kci-" + artiID + ".pdf", URL: normalized,
		Supplier: JobSupplierPrefix + job.JobID, MediaType: "application/pdf", bind: bind,
	}, fetchedAt, string(download))
	if err != nil {
		if errors.Is(err, ErrLeaseLost) || errors.Is(err, ErrJobNotFound) {
			return JobOutcome{}, err
		}
		// ErrDeleted, ErrIntegrity, and the like are not retryable by a browser.
		// Only a fixed code is recorded: the error text can name host paths or
		// registry details, and job status is readable by remote clients. The
		// driver's own evidence (f.Evidence) stays as the contract set it.
		out.State, out.Detail = JobFailed, "storage_failed ("+storeFaultCode(err)+"): the file was downloaded but could not be stored; an operator must check the archive host (disk space, permissions, registry) and retry the job"
		if errors.Is(err, ErrDeleted) {
			out.Detail = "the bytes are quarantined or purged; an operator must restore them first"
		}
	}
	return out, nil
}
