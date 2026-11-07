package trial

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/teslamint/cogvault/internal/archive"
)

const (
	MaxManifestBytes   = 8 << 10
	MaxManifestSources = 5
	MaxTrialReports    = 10
	MaxSourceOriginals = 5
	manifestSupplier   = "cogvault trial selected manifest v1"
	manifestTitle      = "Selected research source manifest"
	manifestKeyPrefix  = "trial-manifest-v1:"
)

var (
	manifestID   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,95}$`)
	sourceID     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,47}$`)
	manifestHash = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)
	captureID    = regexp.MustCompile(`^cap_[a-f0-9]{32}$`)
	reportJobID  = regexp.MustCompile(`^job_[a-f0-9]{32}$`)
	bokQuery     = regexp.MustCompile(`^atchFileId=[0-9a-f]{32}&fileSn=[0-9]{1,3}$`)
	kciQuery     = regexp.MustCompile(`^sereArticleSearchBean\.artiId=ART[0-9]{9}$`)
)

// SourceManifest deliberately has no report body, activity log, filesystem
// inputs, caller approval flag, or inferred research-byte hash.
type SourceManifest struct {
	Version        int              `json:"version"`
	ReportID       string           `json:"report_id"`
	SelectedSHA256 string           `json:"selected_sha256"`
	Sources        []ManifestSource `json:"sources"`
}
type ManifestSource struct {
	ID               string            `json:"id"`
	CitationURL      string            `json:"citation_url"`
	AcquireURL       string            `json:"acquire_url,omitempty"`
	Relation         string            `json:"relation"`
	CitationCount    int               `json:"citation_count"`
	Mode             string            `json:"mode"`
	ExternalOriginal *ExternalOriginal `json:"external_original,omitempty"`
}

// ExternalOriginal is a caller's reference claim, never a trial preservation
// record, research-byte match, or authority to fetch/copy anything.
type ExternalOriginal struct {
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
	Verification string `json:"verification"`
}

// strictManifestJSON rejects duplicate keys, noncanonical field casing and
// nested shapes before decoding into typed values. encoding/json alone accepts
// duplicate keys and case-insensitive field names.
func strictManifestJSON(d *json.Decoder, depth int) error {
	if depth > 6 {
		return archive.ErrInvalid
	}
	tok, err := d.Token()
	if err != nil {
		return archive.ErrInvalid
	}
	if tok == nil {
		return archive.ErrInvalid
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return archive.ErrInvalid
			}
			key, ok := k.(string)
			if !ok || seen[key] {
				return archive.ErrInvalid
			}
			seen[key] = true
			switch key {
			case "version", "report_id", "selected_sha256", "sources", "id", "citation_url", "acquire_url", "relation", "citation_count", "mode", "external_original", "sha256", "size", "verification":
			default:
				return archive.ErrInvalid
			}
			if err := strictManifestJSON(d, depth+1); err != nil {
				return err
			}
		}
		if end, err := d.Token(); err != nil || end != json.Delim('}') {
			return archive.ErrInvalid
		}
		var required []string
		switch depth {
		case 0:
			required = []string{"version", "report_id", "selected_sha256", "sources"}
		case 2:
			required = []string{"id", "citation_url", "relation", "citation_count", "mode"}
		case 3:
			required = []string{"sha256", "size", "verification"}
		default:
			return archive.ErrInvalid
		}
		for _, key := range required {
			if !seen[key] {
				return archive.ErrInvalid
			}
		}

	case '[':
		for d.More() {
			if err := strictManifestJSON(d, depth+1); err != nil {
				return err
			}
		}
		if end, err := d.Token(); err != nil || end != json.Delim(']') {
			return archive.ErrInvalid
		}
	default:
		return archive.ErrInvalid
	}
	return nil
}

func parseManifest(raw string) (SourceManifest, []byte, error) {
	var m SourceManifest
	if len(raw) == 0 || len(raw) > MaxManifestBytes || !utf8.ValidString(raw) {
		return m, nil, archive.ErrInvalid
	}
	d := json.NewDecoder(strings.NewReader(raw))
	if err := strictManifestJSON(d, 0); err != nil {
		return m, nil, archive.ErrInvalid
	}
	if _, err := d.Token(); err != io.EOF {
		return m, nil, archive.ErrInvalid
	}
	d = json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return m, nil, archive.ErrInvalid
	}
	if m.Version != 1 || !manifestID.MatchString(m.ReportID) || strings.HasPrefix(m.ReportID, "cap_") || !manifestHash.MatchString(m.SelectedSHA256) || len(m.Sources) < 1 || len(m.Sources) > MaxManifestSources {
		return m, nil, archive.ErrInvalid
	}
	seen := map[string]bool{}
	for i := range m.Sources {
		src := &m.Sources[i]
		if !sourceID.MatchString(src.ID) || seen[src.ID] || !safeManifestURL(src.CitationURL) || src.CitationCount < 0 || src.CitationCount > 10000 || (src.Relation == "citation" && src.CitationCount == 0) {
			return m, nil, archive.ErrInvalid
		}
		seen[src.ID] = true
		if src.Mode != "manual" && src.Mode != "http" {
			return m, nil, archive.ErrInvalid
		}
		if src.Relation != "citation" && src.Relation != "body_url" {
			return m, nil, archive.ErrInvalid
		}
		if src.AcquireURL != "" && !safeManifestURL(src.AcquireURL) {
			return m, nil, archive.ErrInvalid
		}
		if src.Mode == "http" {
			if src.AcquireURL == "" {
				src.AcquireURL = src.CitationURL
			}
			u, _ := url.Parse(src.AcquireURL)
			citation, _ := url.Parse(src.CitationURL)
			if isKCI(u.Hostname()) || isKCI(citation.Hostname()) {
				return m, nil, archive.ErrInvalid
			}
		}
		if e := src.ExternalOriginal; e != nil {
			if src.Mode != "manual" || !manifestHash.MatchString(e.SHA256) || e.Size < 1 || e.Size > archive.MaxObjectBytes || !oneOf(e.Verification, "unknown", "verified", "incomplete") {
				return m, nil, archive.ErrInvalid
			}
		}
	}
	canonical, err := json.Marshal(m)
	if err != nil || len(canonical) > MaxManifestBytes {
		return m, nil, archive.ErrInvalid
	}
	return m, canonical, nil
}

// safeManifestURL is syntax-only validation of citation metadata, never network
// approval. Queries have only the two fixed, public-identifier shapes below.
// The acquirer independently resolves and rejects nonpublic network addresses.
func safeManifestURL(raw string) bool {
	if len(raw) == 0 || len(raw) > maxURLLength || strings.ContainsAny(raw, "\\\r\n\t %") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.Host != u.Hostname() || u.Fragment != "" || u.RawFragment != "" || u.ForceQuery || u.RawPath != "" || u.String() != raw || !hostPattern.MatchString(u.Host) || len(u.Host) > 253 {
		return false
	}
	if _, err := netip.ParseAddr(u.Host); err == nil {
		return false
	}
	for _, suffix := range []string{".localhost", ".local", ".internal"} {
		if strings.HasSuffix(u.Host, suffix) {
			return false
		}
	}
	if u.RawQuery == "" {
		return true
	}
	return u.Host == "www.bok.or.kr" && u.Path == "/portal/cmmn/file/fileDown.do" && bokQuery.MatchString(u.RawQuery) || u.Host == "www.kci.go.kr" && u.Path == "/kciportal/ci/sereArticleSearch/ciSereArtiView.kci" && kciQuery.MatchString(u.RawQuery)
}

func isKCI(host string) bool { return host == "kci.go.kr" || strings.HasSuffix(host, ".kci.go.kr") }

func oneOf(s string, values ...string) bool {
	for _, v := range values {
		if s == v {
			return true
		}
	}
	return false
}
func digestBytes(b []byte) string { h := sha256.Sum256(b); return "sha256:" + hex.EncodeToString(h[:]) }

// ImportManifest stores a bounded canonical manifest, not a research report
// body. Its stable report key is immutable and deduplicated. Importing never
// queues a job, touches a source file, fetches, or grants host/URL approval.
func (s *Service) ImportManifest(ctx context.Context, raw string) (archive.Result, error) {
	m, b, err := parseManifest(raw)
	if err != nil {
		return archive.Result{}, err
	}
	unlock, err := s.claimLock(ctx)
	if err != nil {
		return archive.Result{}, err
	}
	defer unlock()
	key := manifestKeyPrefix + m.ReportID
	prior, err := s.A.ReadReport(ctx, key, manifestSupplier, MaxManifestBytes)
	if err == nil {
		if prior.Title != manifestTitle || prior.RequestKey != key {
			return archive.Result{}, archive.ErrInvalid
		}
		if !bytes.Equal(prior.Content, b) {
			return archive.Result{}, archive.ErrConflict
		}
		return archive.Result{CaptureID: prior.CaptureID, ObjectID: prior.ObjectID, Existing: true}, nil
	}
	if !errors.Is(err, archive.ErrNotFound) {
		return archive.Result{}, err
	}
	n, err := s.A.CountReports(ctx, manifestSupplier)
	if err != nil {
		return archive.Result{}, err
	}
	if n >= MaxTrialReports {
		return archive.Result{}, ErrLimit
	}
	cites := make([]string, 0, len(m.Sources))
	for _, src := range m.Sources {
		cites = append(cites, src.CitationURL)
	}
	return s.A.ImportReport(ctx, archive.ReportRequest{Key: key, Content: bytes.NewReader(b), OriginalName: "selected-sources.json", Supplier: manifestSupplier, Title: manifestTitle, Cites: cites})
}

func (s *Service) loadManifest(ctx context.Context, ref string) (SourceManifest, string, error) {
	if !manifestID.MatchString(ref) {
		return SourceManifest{}, "", archive.ErrInvalid
	}
	lookup := ref
	if !captureID.MatchString(ref) {
		lookup = manifestKeyPrefix + ref
	}
	obj, err := s.A.ReadReport(ctx, lookup, manifestSupplier, MaxManifestBytes)
	if err != nil {
		return SourceManifest{}, "", err
	}
	m, b, err := parseManifest(string(obj.Content))
	if err != nil || !bytes.Equal(b, obj.Content) || obj.Title != manifestTitle || obj.RequestKey != manifestKeyPrefix+m.ReportID {
		return SourceManifest{}, "", archive.ErrInvalid
	}
	if !captureID.MatchString(ref) && m.ReportID != ref {
		return SourceManifest{}, "", archive.ErrInvalid
	}
	return m, obj.CaptureID, nil
}
func findSource(m SourceManifest, id string) (ManifestSource, error) {
	for _, src := range m.Sources {
		if src.ID == id {
			return src, nil
		}
	}
	return ManifestSource{}, archive.ErrInvalid
}

func (s *Service) SubmitSource(ctx context.Context, rawURL, label, reportID, sourceID string) (archive.Job, bool, error) {
	m, capID, err := s.loadManifest(ctx, reportID)
	if err != nil {
		return archive.Job{}, false, err
	}
	src, err := findSource(m, sourceID)
	if err != nil {
		return archive.Job{}, false, err
	}
	if src.Mode != "http" || src.AcquireURL != rawURL || !s.reportURLAllowed(rawURL) {
		return archive.Job{}, false, ErrPolicy
	}
	unlock, err := s.claimLock(ctx)
	if err != nil {
		return archive.Job{}, false, err
	}
	defer unlock()
	jobs, err := s.A.GetReportJobs(ctx, capID, 51)
	if err != nil {
		return archive.Job{}, false, err
	}
	if len(jobs) > 50 {
		return archive.Job{}, false, ErrLimit
	}
	for _, j := range jobs {
		if j.ClaimRef != "source:"+src.ID {
			continue
		}
		if j.URL != rawURL || j.Executor != archive.ExecutorHTTP || j.SeenSHA256 != "" {
			return archive.Job{}, false, archive.ErrConflict
		}
		return j, true, nil
	}
	return s.submitRequestLocked(ctx, archive.JobRequest{URL: rawURL, Submitter: label, ReportCaptureID: capID, ClaimRef: "source:" + src.ID})
}

func (s *Service) validateReportJob(ctx context.Context, job *archive.Job) error {
	if job == nil || !captureID.MatchString(job.ReportCaptureID) {
		return ErrPolicy
	}
	m, capID, err := s.loadManifest(ctx, job.ReportCaptureID)
	if err != nil {
		return ErrPolicy
	}
	src, err := findSource(m, strings.TrimPrefix(job.ClaimRef, "source:"))
	if err != nil || job.ClaimRef != "source:"+src.ID || job.ReportCaptureID != capID || job.Executor != archive.ExecutorHTTP || src.Mode != "http" || src.AcquireURL != job.URL || job.SeenSHA256 != "" || !s.reportURLAllowed(job.URL) {
		return ErrPolicy
	}
	return nil
}

// OriginalImport is operator-only byte input. It has no filesystem or remote
// transport facility. Verification is over the complete supplied stream before
// a capture is stored; external completeness claims cannot elevate it.
type OriginalImport struct {
	Content           io.Reader
	ExpectedSHA256    string
	ExpectedSize      int64
	MediaType         string
	Completeness      string
	AcquisitionMethod string
}

func originalSupplier(reportID, sourceID string) string {
	return "cogvault trial original v1:" + reportID + ":" + sourceID
}

func (s *Service) ImportOriginal(ctx context.Context, reportID, sourceID string, req OriginalImport) (archive.Result, error) {
	m, capID, err := s.loadManifest(ctx, reportID)
	if err != nil {
		return archive.Result{}, err
	}
	src, err := findSource(m, sourceID)
	if err != nil {
		return archive.Result{}, err
	}
	if req.Content == nil || !manifestHash.MatchString(req.ExpectedSHA256) || req.ExpectedSize < 1 || req.ExpectedSize > archive.MaxObjectBytes || !oneOf(req.MediaType, "application/pdf", "text/html") || !oneOf(req.Completeness, "unknown", "incomplete") || !oneOf(req.AcquisitionMethod, "existing_file", "browser_download", "direct_https") {
		return archive.Result{}, archive.ErrInvalid
	}
	b, err := io.ReadAll(io.LimitReader(req.Content, req.ExpectedSize+1))
	if err != nil {
		return archive.Result{}, archive.ErrInvalid
	}
	if int64(len(b)) != req.ExpectedSize || digestBytes(b) != req.ExpectedSHA256 {
		return archive.Result{}, archive.ErrIntegrity
	}
	media := strings.Split(http.DetectContentType(b), ";")[0]
	if media != req.MediaType {
		return archive.Result{}, archive.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return archive.Result{}, err
	}
	unlock, err := s.claimLock(ctx)
	if err != nil {
		return archive.Result{}, err
	}
	defer unlock()
	supplier := originalSupplier(capID, src.ID)
	caps, err := s.A.GetCapturesBySupplier(ctx, supplier, MaxSourceOriginals+1)
	if err != nil {
		return archive.Result{}, err
	}
	for _, c := range caps {
		if c.ObjectID == req.ExpectedSHA256 {
			if c.Completeness == "unavailable" {
				return archive.Result{}, archive.ErrDeleted
			}
			if c.MediaType != req.MediaType || c.Completeness != req.Completeness || c.Title != req.AcquisitionMethod {
				return archive.Result{}, archive.ErrConflict
			}
			// CaptureSource re-verifies existing object bytes before reporting a repeat.
			return s.storeOriginal(ctx, capID, src, req, b)
		}
	}
	if len(caps) >= MaxSourceOriginals {
		return archive.Result{}, ErrLimit
	}
	return s.storeOriginal(ctx, capID, src, req, b)
}
func (s *Service) storeOriginal(ctx context.Context, reportID string, src ManifestSource, req OriginalImport, b []byte) (archive.Result, error) {
	basis := ""
	if req.Completeness == "incomplete" {
		basis = "caller supplied partial response"
	}
	key := "trial-original-v1:" + reportID + ":" + src.ID + ":" + strings.TrimPrefix(req.ExpectedSHA256, "sha256:")
	ext := ".pdf"
	if req.MediaType == "text/html" {
		ext = ".html"
	}
	return s.A.CaptureSource(ctx, archive.SourceRequest{Key: key, Content: bytes.NewReader(b), OriginalName: "supplied-original" + ext, URL: src.CitationURL, Supplier: originalSupplier(reportID, src.ID), Title: req.AcquisitionMethod, MediaType: req.MediaType, Completeness: req.Completeness, Basis: basis})
}

// ReportStatus contains only validated selected-source metadata and enum-based
// results. The selected file digest is a caller assertion, not verified report
// coverage, and external references never count as trial-preserved originals.
type ReportStatus struct {
	ReportID       string             `json:"report_id"`
	CaptureID      string             `json:"capture_id"`
	SelectedSHA256 string             `json:"selected_sha256"`
	Coverage       string             `json:"coverage"`
	Sources        []ReportSourceView `json:"sources"`
	Aggregate      ReportAggregate    `json:"aggregate"`
}
type ReportAggregate struct {
	Selected            int `json:"selected"`
	Pending             int `json:"pending"`
	PolicyBlocked       int `json:"policy_blocked"`
	Manual              int `json:"manual"`
	Queued              int `json:"queued"`
	Running             int `json:"running"`
	Held                int `json:"held"`
	Acquired            int `json:"acquired"`
	Failed              int `json:"failed"`
	HumanActionRequired int `json:"human_action_required"`
	StoredCaptures      int `json:"stored_captures"`
	StoredSources       int `json:"stored_sources"`
	MissingSources      int `json:"missing_sources"`
	ExternalReferences  int `json:"external_references"`
}
type ReportSourceView struct {
	ID               string                `json:"id"`
	CitationURL      string                `json:"citation_url"`
	AcquireURL       string                `json:"acquire_url,omitempty"`
	Relation         string                `json:"relation"`
	CitationCount    int                   `json:"citation_count"`
	Mode             string                `json:"mode"`
	State            string                `json:"state"`
	JobID            string                `json:"job_id,omitempty"`
	AttemptsMade     int                   `json:"attempts_made"`
	FailureReason    string                `json:"failure_reason,omitempty"`
	ExternalOriginal *ExternalOriginalView `json:"external_original,omitempty"`
	Captures         []ReportCaptureView   `json:"captures"`
}
type ExternalOriginalView struct {
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
	Verification string `json:"verification"`
	Preservation string `json:"preservation"`
	Evidence     string `json:"evidence"`
}
type ReportCaptureView struct {
	CaptureID         string `json:"capture_id"`
	SHA256            string `json:"sha256"`
	Size              int64  `json:"size"`
	MediaType         string `json:"media_type"`
	Completeness      string `json:"completeness"`
	Provenance        string `json:"provenance"`
	AcquisitionMethod string `json:"acquisition_method"`
	RecordedAt        string `json:"recorded_at"`
}

func reportCapture(c archive.Capture, provenance, method string) (ReportCaptureView, bool) {
	if c.Kind != "source" || !captureID.MatchString(c.CaptureID) || !manifestHash.MatchString(c.ObjectID) || c.Size < 1 || c.Size > archive.MaxObjectBytes || !oneOf(c.MediaType, "application/pdf", "text/html", "application/xhtml+xml") || !oneOf(c.Completeness, "unknown", "incomplete", "verified") {
		return ReportCaptureView{}, false
	}
	recorded, err := time.Parse(time.RFC3339Nano, c.RecordedAt)
	if err != nil || len(c.RecordedAt) > 35 {
		return ReportCaptureView{}, false
	}
	return ReportCaptureView{RecordedAt: recorded.UTC().Format(time.RFC3339Nano), CaptureID: c.CaptureID, SHA256: c.ObjectID, Size: c.Size, MediaType: c.MediaType, Completeness: c.Completeness, Provenance: provenance, AcquisitionMethod: method}, true
}

func (s *Service) ReportStatus(ctx context.Context, ref string) (ReportStatus, error) {
	m, capID, err := s.loadManifest(ctx, ref)
	if err != nil {
		return ReportStatus{}, err
	}
	jobs, err := s.A.GetReportJobs(ctx, capID, 51)
	if err != nil {
		return ReportStatus{}, err
	}
	if len(jobs) > 50 {
		return ReportStatus{}, ErrLimit
	}
	out := ReportStatus{ReportID: m.ReportID, CaptureID: capID, SelectedSHA256: m.SelectedSHA256, Coverage: "selected_sources_only", Sources: []ReportSourceView{}}
	for _, src := range m.Sources {
		v := ReportSourceView{ID: src.ID, CitationURL: src.CitationURL, AcquireURL: src.AcquireURL, Relation: src.Relation, CitationCount: src.CitationCount, Mode: src.Mode, State: "pending", Captures: []ReportCaptureView{}}
		if src.Mode == "manual" {
			v.State = "manual"
		} else if !s.reportURLAllowed(src.AcquireURL) {
			v.State = "policy_blocked"
		}
		if e := src.ExternalOriginal; e != nil {
			v.ExternalOriginal = &ExternalOriginalView{SHA256: e.SHA256, Size: e.Size, Verification: e.Verification, Preservation: "not_preserved_in_trial", Evidence: "reference_claim"}
			out.Aggregate.ExternalReferences++
		}
		// Only manifest-associated jobs are projected. Generic archive jobs cannot
		// impersonate a different source or claim research-time identity.
		for _, j := range jobs {
			if src.Mode != "http" || j.ClaimRef != "source:"+src.ID || j.URL != src.AcquireURL || j.Executor != archive.ExecutorHTTP || j.SeenSHA256 != "" || !reportJobID.MatchString(j.JobID) {
				continue
			}
			if !oneOf(j.State, "queued", "running", "held", "acquired", "failed", "human_action_required") {
				continue
			}
			v.JobID = j.JobID
			v.State = j.State
			v.AttemptsMade = min(max(j.AttemptsMade, 0), 10000)
			if j.State == "failed" {
				v.FailureReason = "collection_failed"
			} else if j.State == "human_action_required" {
				v.FailureReason = "human_action_required"
			} else {
				v.FailureReason = ""
			}
			if j.State == "failed" || j.State == "human_action_required" || (j.State == "queued" && j.AttemptsMade > 0) {
				reason, err := s.A.JobFailureReason(ctx, j.JobID)
				if err != nil {
					return ReportStatus{}, err
				}
				if reason != "" {
					v.FailureReason = reason
				}
			}
			if j.ResultCaptureID != "" {
				c, e := s.A.CaptureMetadata(ctx, j.ResultCaptureID)
				if errors.Is(e, archive.ErrNotFound) {
					v.FailureReason = "capture_unavailable"
					continue
				}
				if e != nil {
					return ReportStatus{}, e
				}
				// The collector supplier prevents linking an arbitrary pre-existing source
				// to a job and calling it a fetched original.
				if c.Supplier != archive.JobSupplierPrefix+j.JobID {
					continue
				}
				if cv, ok := reportCapture(c, archive.ProvRecovered, "http"); ok {
					v.Captures = appendUniqueCapture(v.Captures, cv)
				}
			}
		}
		caps, err := s.A.GetCapturesBySupplier(ctx, originalSupplier(capID, src.ID), MaxSourceOriginals+1)
		if err != nil {
			return ReportStatus{}, err
		}
		if len(caps) > MaxSourceOriginals {
			return ReportStatus{}, ErrLimit
		}
		for _, c := range caps {
			if !oneOf(c.Title, "existing_file", "browser_download", "direct_https") {
				continue
			}
			if cv, ok := reportCapture(c, "imported_existing", c.Title); ok {
				v.Captures = appendUniqueCapture(v.Captures, cv)
			}
		}
		out.Aggregate.StoredCaptures += len(v.Captures)
		if len(v.Captures) > 0 {
			out.Aggregate.StoredSources++
			if oneOf(v.State, "manual", "pending", "policy_blocked") {
				v.State = "stored"
			}
		} else {
			out.Aggregate.MissingSources++
		}
		if v.ExternalOriginal != nil {
			for _, c := range v.Captures {
				if c.SHA256 == v.ExternalOriginal.SHA256 {
					v.ExternalOriginal.Preservation = "matching_copy_preserved_in_trial"
					break
				}
			}
		}
		out.Aggregate.Selected++
		switch v.State {
		case "pending":
			out.Aggregate.Pending++
		case "policy_blocked":
			out.Aggregate.PolicyBlocked++
		case "manual":
			out.Aggregate.Manual++
		case "queued":
			out.Aggregate.Queued++
		case "running":
			out.Aggregate.Running++
		case "held":
			out.Aggregate.Held++
		case "acquired":
			out.Aggregate.Acquired++
		case "failed":
			out.Aggregate.Failed++
		case "human_action_required":
			out.Aggregate.HumanActionRequired++
		}
		out.Sources = append(out.Sources, v)
	}
	return out, nil
}
func appendUniqueCapture(caps []ReportCaptureView, c ReportCaptureView) []ReportCaptureView {
	for _, prior := range caps {
		if prior.CaptureID == c.CaptureID {
			return caps
		}
	}
	return append(caps, c)
}
