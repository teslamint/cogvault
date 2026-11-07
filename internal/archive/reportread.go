package archive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"regexp"
	"strings"
)

// ReportObject is a bounded, hash-verified report object. It is for internal
// report schemas, never a generic remote object download endpoint.
type ReportObject struct {
	CaptureID  string
	ObjectID   string
	Content    []byte
	Title      string
	RequestKey string
}

var reportObjectHash = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// ReadReport accepts a capture ID or exact request key, requires a fixed caller
// supplier, and reads only active report objects of at most 8 KiB. It checks the
// actual bytes, not just registry metadata. No path is accepted or returned.
func (a *Archive) ReadReport(ctx context.Context, ref, supplier string, maxBytes int64) (ReportObject, error) {
	if ref == "" || len(ref) > 200 || supplier == "" || len(supplier) > 200 || maxBytes < 1 || maxBytes > 8192 {
		return ReportObject{}, ErrInvalid
	}
	unlock, err := a.lock(ctx, true)
	if err != nil {
		return ReportObject{}, err
	}
	defer unlock()
	var out ReportObject
	var size int64
	err = a.db.QueryRowContext(ctx, `SELECT c.capture_id,c.object_id,o.size,c.title,c.request_key FROM captures c JOIN objects o ON o.object_id=c.object_id WHERE (c.capture_id=? OR c.request_key=?) AND c.kind='report' AND c.supplier=? AND o.state='active'`, ref, ref, supplier).Scan(&out.CaptureID, &out.ObjectID, &size, &out.Title, &out.RequestKey)
	if errors.Is(err, sql.ErrNoRows) {
		return ReportObject{}, ErrNotFound
	}
	if err != nil {
		return ReportObject{}, err
	}
	if size > maxBytes {
		return ReportObject{}, ErrTooLarge
	}
	if size < 0 || !reportObjectHash.MatchString(out.ObjectID) {
		return ReportObject{}, ErrIntegrity
	}
	f, err := os.OpenFile(a.objectPath(strings.TrimPrefix(out.ObjectID, "sha256:")), os.O_RDONLY|noFollow, 0)
	if err != nil {
		return ReportObject{}, ErrIntegrity
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() != size {
		return ReportObject{}, ErrIntegrity
	}
	b, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	if err != nil {
		return ReportObject{}, ErrIntegrity
	}
	sum := sha256.Sum256(b)
	if int64(len(b)) != size || "sha256:"+hex.EncodeToString(sum[:]) != out.ObjectID {
		return ReportObject{}, ErrIntegrity
	}
	if err := ctx.Err(); err != nil {
		return ReportObject{}, err
	}
	out.Content = b
	return out, nil
}

// CountReports counts all report registrations, including quarantined objects,
// so deleting/quarantining bytes does not reset an admission bound.
func (a *Archive) CountReports(ctx context.Context, supplier string) (int, error) {
	if supplier == "" || len(supplier) > 200 {
		return 0, ErrInvalid
	}
	var n int
	err := a.db.QueryRowContext(ctx, `SELECT count(*) FROM captures WHERE kind='report' AND supplier=?`, supplier).Scan(&n)
	return n, err
}

// GetReportJobs returns at most limit jobs for one report, without attempts,
// responses, worker IDs or free-text labels. Callers validate association fields
// against their own schema and must not serialize this internal record directly.
func (a *Archive) GetReportJobs(ctx context.Context, reportID string, limit int) ([]Job, error) {
	if limit < 1 || limit > 100 || len(reportID) > 100 {
		return nil, ErrInvalid
	}
	rows, err := a.db.QueryContext(ctx, `SELECT job_id,url,executor,COALESCE(report_capture_id,''),claim_ref,seen_sha256,state,provenance,attempts_made,max_attempts,COALESCE(result_capture_id,'') FROM jobs WHERE report_capture_id=? ORDER BY created_at,job_id LIMIT ?`, reportID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		var j Job
		if err := rows.Scan(&j.JobID, &j.URL, &j.Executor, &j.ReportCaptureID, &j.ClaimRef, &j.SeenSHA256, &j.State, &j.Provenance, &j.AttemptsMade, &j.MaxAttempts, &j.ResultCaptureID); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// CaptureMetadata is a single bounded registry projection, with no relations,
// response records, original names, source URLs, or arbitrary completeness basis.
func (a *Archive) CaptureMetadata(ctx context.Context, id string) (Capture, error) {
	var c Capture
	err := a.db.QueryRowContext(ctx, `SELECT c.capture_id,c.kind,c.object_id,o.size,c.media_type,c.supplier,c.title,c.completeness,c.recorded_at FROM captures c JOIN objects o ON o.object_id=c.object_id WHERE c.capture_id=? AND o.state='active'`, id).Scan(&c.CaptureID, &c.Kind, &c.ObjectID, &c.Size, &c.MediaType, &c.Supplier, &c.Title, &c.Completeness, &c.RecordedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Capture{}, ErrNotFound
	}
	return c, err
}

// GetCapturesBySupplier returns bounded original metadata only, including
// quarantined registrations for admission accounting. Deleted bytes cannot be
// reported as preserved: their completeness is returned as unavailable.
func (a *Archive) GetCapturesBySupplier(ctx context.Context, supplier string, limit int) ([]Capture, error) {
	if supplier == "" || len(supplier) > 200 || limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	rows, err := a.db.QueryContext(ctx, `SELECT c.capture_id,c.kind,c.object_id,o.size,c.media_type,c.supplier,c.title,CASE WHEN o.state='active' THEN c.completeness ELSE 'unavailable' END,c.recorded_at FROM captures c JOIN objects o ON o.object_id=c.object_id WHERE c.kind='source' AND c.supplier=? ORDER BY c.recorded_at,c.capture_id LIMIT ?`, supplier, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Capture{}
	for rows.Next() {
		var c Capture
		if err := rows.Scan(&c.CaptureID, &c.Kind, &c.ObjectID, &c.Size, &c.MediaType, &c.Supplier, &c.Title, &c.Completeness, &c.RecordedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// JobFailureReason maps at most 500 characters of the latest recorded attempt
// to fixed codes. No arbitrary attempt text, response, header or address leaves
// this helper. Prefixes are the collector/worker's stable diagnostic vocabulary,
// not proof that an untrusted external caller's claim is true.
func (a *Archive) JobFailureReason(ctx context.Context, id string) (string, error) {
	var detail string
	err := a.db.QueryRowContext(ctx, `SELECT substr(detail,1,500) FROM job_attempts WHERE job_id=? ORDER BY attempt_id DESC LIMIT 1`, id).Scan(&detail)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	switch {
	case strings.HasPrefix(detail, "rate_limited: HTTP 429;"):
		return "rate_limited", nil
	case strings.HasPrefix(detail, "access restricted (HTTP "):
		return "access_restricted", nil
	case strings.HasPrefix(detail, "media_not_allowed:"), detail == "the response is not a PDF":
		return "media_not_allowed", nil
	case strings.HasPrefix(detail, "trial policy:"), detail == "destination is not a public address", strings.HasPrefix(detail, "a host is not on the allowed list ("), strings.HasPrefix(detail, "a URL is outside the allowed policy ("), strings.HasPrefix(detail, "the URL or a redirect was refused ("):
		return "policy_refused", nil
	case detail == "network error: timed out":
		return "network_timeout", nil
	default:
		return "collection_failed", nil
	}
}
