package archive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"time"
)

// Durable collection jobs (ADR 0027 addendum, SPEC §9.16 "Collection
// jobs"). A job names one public URL to acquire. The queue lives in the
// archive registry, so it survives the submitter, the chat, and the
// process: any client that knows the job ID reads the same state, and an
// executor that stops mid-job loses its lease and the job resumes.
//
// The queue stores no bytes and no credentials. Bytes go through the same
// immutable object store and capture registry as every other capture; a job
// only points at the capture it produced. Attempts are append-only.

const (
	jobStateQueued  = "queued"
	jobStateRunning = "running"
	// JobHeld: bytes were stored but are not hand-off eligible (completeness
	// is not verified). The public HTTP executor always ends here on success.
	JobHeld = "held"
	// JobAcquired: bytes were stored with verified completeness. Only an
	// executor that can show checkable evidence reports this.
	JobAcquired = "acquired"
	JobFailed   = "failed"
	JobHuman    = "human_action_required"
	// JobRetry is an executor outcome, not a stored state: try again later.
	JobRetry = "retry"

	// ExecutorHTTP is the public HTTP executor in this package. ExecutorBrowser
	// is reserved for an interactive official-UI executor that is not
	// implemented here; it claims jobs through ClaimJob/CompleteJob.
	ExecutorHTTP    = "http"
	ExecutorBrowser = "browser"

	// ProvSeen: the acquired bytes hash to the digest the submitter says it
	// read during research. ProvRecovered: bytes obtained later with no
	// evidence they are the bytes the research read.
	ProvSeen      = "seen_during_research"
	ProvRecovered = "recovered_after_research"

	defaultJobAttempts = 5
	maxJobAttempts     = 20
	jobTimeLayout      = "2006-01-02T15:04:05.000000000Z" // fixed width: compares as text
	jobBackoffBase     = time.Minute
	jobBackoffCap      = time.Hour
	// JobSupplierPrefix marks captures made by the collector.
	JobSupplierPrefix = "cogvault collector job "
)

var (
	ErrJobNotFound = errors.New("job not found")
	// ErrLeaseLost: the job is no longer leased to this owner (the lease
	// expired and the job was reclaimed, or it is already finished).
	ErrLeaseLost = errors.New("job is not leased to this owner")
	ErrJobState  = errors.New("job is not in a state that allows this action")

	seenDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

const v6Tables = `
CREATE TABLE IF NOT EXISTS jobs (
 job_id TEXT PRIMARY KEY,
 dedupe_key TEXT NOT NULL UNIQUE,
 url TEXT NOT NULL,
 executor TEXT NOT NULL CHECK (executor IN ('http','browser')),
 submitter TEXT NOT NULL,
 edition_hint TEXT NOT NULL,
 report_capture_id TEXT REFERENCES captures(capture_id),
 claim_ref TEXT NOT NULL,
 seen_sha256 TEXT NOT NULL,
 state TEXT NOT NULL CHECK (state IN ('queued','running','acquired','held','failed','human_action_required')),
 provenance TEXT NOT NULL CHECK (provenance IN ('','seen_during_research','recovered_after_research')),
 attempts_made INTEGER NOT NULL,
 budget INTEGER NOT NULL,
 max_attempts INTEGER NOT NULL,
 not_before TEXT NOT NULL,
 lease_owner TEXT NOT NULL,
 lease_until TEXT NOT NULL,
 started_at TEXT NOT NULL,
 result_capture_id TEXT REFERENCES captures(capture_id),
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS jobs_claim ON jobs(state, executor, not_before);
CREATE TABLE IF NOT EXISTS job_attempts (
 attempt_id INTEGER PRIMARY KEY AUTOINCREMENT,
 job_id TEXT NOT NULL REFERENCES jobs(job_id),
 number INTEGER NOT NULL,
 owner TEXT NOT NULL,
 started_at TEXT NOT NULL,
 finished_at TEXT NOT NULL,
 outcome TEXT NOT NULL CHECK (outcome IN ('acquired','held','retry','failed','human_action_required','lease_expired')),
 detail TEXT NOT NULL,
 response TEXT NOT NULL,
 provenance TEXT NOT NULL CHECK (provenance IN ('','seen_during_research','recovered_after_research')),
 capture_id TEXT REFERENCES captures(capture_id));
CREATE INDEX IF NOT EXISTS job_attempts_job ON job_attempts(job_id);
CREATE INDEX IF NOT EXISTS job_attempts_capture ON job_attempts(capture_id);
`

// JobRequest submits one URL for collection.
type JobRequest struct {
	URL string
	// Key is an optional idempotency key. When empty, the key is derived from
	// the URL, edition hint, report, and claim reference, so the same request
	// from any client returns the same job.
	Key string
	// Submitter labels who asked. It is not authenticated identity: the
	// transport (CLI host access, MCP auth) is the authority.
	Submitter string
	// EditionHint is free text naming the edition wanted (volume, version,
	// year). The collector records it; it never selects among editions.
	EditionHint string
	// ReportCaptureID links the job to an imported research report capture.
	ReportCaptureID string
	// ClaimRef is the claim or locator the source supports (free text).
	ClaimRef string
	// SeenSHA256 is "sha256:<hex>" of the bytes the research session read,
	// when the submitter has them. Without it, acquired bytes are
	// recovered_after_research.
	SeenSHA256 string
	// Executor is ExecutorHTTP (default) or ExecutorBrowser.
	Executor    string
	MaxAttempts int
}

// Job is one collection job with its append-only attempt history.
type Job struct {
	JobID           string       `json:"job_id"`
	URL             string       `json:"url"`
	Executor        string       `json:"executor"`
	Submitter       string       `json:"submitter"`
	EditionHint     string       `json:"edition_hint,omitempty"`
	ReportCaptureID string       `json:"report_capture_id,omitempty"`
	ClaimRef        string       `json:"claim_ref,omitempty"`
	SeenSHA256      string       `json:"seen_sha256,omitempty"`
	State           string       `json:"state"`
	Provenance      string       `json:"provenance,omitempty"`
	AttemptsMade    int          `json:"attempts_made"`
	MaxAttempts     int          `json:"max_attempts"`
	NotBefore       string       `json:"not_before"`
	LeaseOwner      string       `json:"lease_owner,omitempty"`
	LeaseUntil      string       `json:"lease_until,omitempty"`
	ResultCaptureID string       `json:"result_capture_id,omitempty"`
	CreatedAt       string       `json:"created_at"`
	UpdatedAt       string       `json:"updated_at"`
	Attempts        []JobAttempt `json:"attempts"`
	budget          int
	started         string
}

// JobAttempt is one executor try, recorded when it ends.
type JobAttempt struct {
	Number     int    `json:"number"`
	Owner      string `json:"owner"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Outcome    string `json:"outcome"`
	Detail     string `json:"detail"`
	// Response is the acquisition record (requested/final URL, hops with the
	// checked address, selected headers) when the executor made a request.
	Response  string `json:"response,omitempty"`
	CaptureID string `json:"capture_id,omitempty"`
}

// Collection ties a capture to the job that produced it. Provenance says
// whether the bytes are those the research read or were recovered later.
type Collection struct {
	JobID           string `json:"job_id"`
	Provenance      string `json:"provenance"`
	Submitter       string `json:"submitter"`
	ReportCaptureID string `json:"report_capture_id,omitempty"`
	ClaimRef        string `json:"claim_ref,omitempty"`
}

// JobOutcome is what an executor reports for one claimed job.
type JobOutcome struct {
	// State is JobHeld, JobAcquired, JobFailed, JobHuman, or JobRetry.
	State string
	// Detail is a short reason; it must not contain a refused URL or secret.
	Detail    string
	Response  string
	CaptureID string
}

func jobTime(t time.Time) string { return t.UTC().Format(jobTimeLayout) }

func jobBackoff(attempts int) time.Duration {
	d := jobBackoffBase
	for i := 1; i < attempts && d < jobBackoffCap; i++ {
		d *= 2
	}
	return min(d, jobBackoffCap)
}

// SubmitJob queues one URL. A repeated request returns the existing job
// (existing is true) and changes nothing. The URL gets the checks of the
// chosen executor, so a refused URL never enters the queue: the HTTP
// executor takes the same checks as an acquisition (http(s), no credentials,
// query, or fragment); the browser executor takes only a KCI article detail
// page (CheckBrowserURL), the one place a query is admitted, because that
// query is the article ID and the host is fixed.
func (a *Archive) SubmitJob(ctx context.Context, req JobRequest) (job Job, existing bool, err error) {
	exec := req.Executor
	if exec == "" {
		exec = ExecutorHTTP
	}
	if exec != ExecutorHTTP && exec != ExecutorBrowser {
		return Job{}, false, fmt.Errorf("%w: executor %q (want http or browser)", ErrInvalid, exec)
	}
	var u *url.URL
	if exec == ExecutorBrowser {
		normalized, _, perr := CheckBrowserURL(req.URL)
		if perr != nil {
			return Job{}, false, perr
		}
		u, _ = url.Parse(normalized)
	} else {
		var perr error
		if u, perr = url.Parse(req.URL); perr != nil {
			return Job{}, false, fmt.Errorf("%w: unparsable URL", ErrInvalid)
		}
		if err := checkAcquireURL(u); err != nil {
			return Job{}, false, err
		}
	}
	if req.Submitter == "" {
		return Job{}, false, fmt.Errorf("%w: submitter is required", ErrInvalid)
	}
	if req.SeenSHA256 != "" && !seenDigest.MatchString(req.SeenSHA256) {
		return Job{}, false, fmt.Errorf("%w: seen_sha256 must be sha256:<64 hex>", ErrInvalid)
	}
	budget := req.MaxAttempts
	switch {
	case budget == 0:
		budget = defaultJobAttempts
	case budget < 0 || budget > maxJobAttempts:
		return Job{}, false, fmt.Errorf("%w: max attempts must be 1 to %d", ErrInvalid, maxJobAttempts)
	}
	if err := checkKey(req.Key); err != nil {
		return Job{}, false, err
	}
	if err := checkText(req.Submitter, req.EditionHint, req.ClaimRef); err != nil {
		return Job{}, false, err
	}
	dedupe := "key:" + req.Key
	if req.Key == "" {
		sum := sha256.Sum256([]byte("cogvault-job-request-v1\x00" + u.String() + "\x00" + exec + "\x00" + req.EditionHint + "\x00" + req.ReportCaptureID + "\x00" + req.ClaimRef))
		dedupe = "digest:" + hex.EncodeToString(sum[:])
	}
	idSum := sha256.Sum256([]byte("cogvault-job-id-v1\x00" + dedupe))
	id := "job_" + hex.EncodeToString(idSum[:16])

	unlock, err := a.lock(ctx, false)
	if err != nil {
		return Job{}, false, err
	}
	defer unlock()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, false, err
	}
	defer tx.Rollback()
	var prior string
	switch err := tx.QueryRowContext(ctx, `SELECT url FROM jobs WHERE job_id = ?`, id).Scan(&prior); {
	case err == nil:
		if prior != u.String() {
			return Job{}, false, fmt.Errorf("%w: key %q", ErrConflict, req.Key)
		}
		j, err := readJob(ctx, tx, id)
		return j, true, err
	case !errors.Is(err, sql.ErrNoRows):
		return Job{}, false, err
	}
	var report any
	if req.ReportCaptureID != "" {
		var kind string
		err := tx.QueryRowContext(ctx, `SELECT c.kind FROM captures c JOIN objects o ON o.object_id = c.object_id AND o.state = 'active' WHERE c.capture_id = ?`, req.ReportCaptureID).Scan(&kind)
		if errors.Is(err, sql.ErrNoRows) {
			return Job{}, false, fmt.Errorf("%w: %s", ErrNotFound, req.ReportCaptureID)
		}
		if err != nil {
			return Job{}, false, err
		}
		if kind != "report" {
			return Job{}, false, fmt.Errorf("%w: %s is not a report capture", ErrInvalid, req.ReportCaptureID)
		}
		report = req.ReportCaptureID
	}
	now := jobTime(a.now())
	if _, err := tx.ExecContext(ctx, `INSERT INTO jobs (job_id, dedupe_key, url, executor, submitter, edition_hint, report_capture_id, claim_ref, seen_sha256, state, provenance, attempts_made, budget, max_attempts, not_before, lease_owner, lease_until, started_at, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,'queued','',0,?,?,?, '', '', '', ?, ?)`,
		id, dedupe, u.String(), exec, req.Submitter, req.EditionHint, report, req.ClaimRef, req.SeenSHA256, budget, budget, now, now, now); err != nil {
		return Job{}, false, err
	}
	j, err := readJob(ctx, tx, id)
	if err != nil {
		return Job{}, false, err
	}
	return j, false, tx.Commit()
}

// GetJob returns one job with its attempts. A running job whose lease has
// passed still reads as running until an executor reclaims it.
func (a *Archive) GetJob(ctx context.Context, id string) (Job, error) {
	unlock, err := a.lock(ctx, false)
	if err != nil {
		return Job{}, err
	}
	defer unlock()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	return readJob(ctx, tx, id)
}

// RetryJob puts a finished-without-bytes job (failed, human_action_required)
// or a held one back in the queue with a fresh attempt budget. Attempt
// numbers keep counting and the history is kept. It is an operator or
// submitter action; the collector never uses credentials, so a retry of an
// access-restricted job fails the same way unless access changed.
func (a *Archive) RetryJob(ctx context.Context, id string) (Job, error) {
	unlock, err := a.lock(ctx, false)
	if err != nil {
		return Job{}, err
	}
	defer unlock()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	j, err := readJob(ctx, tx, id)
	if err != nil {
		return Job{}, err
	}
	if j.State != JobFailed && j.State != JobHuman && j.State != JobHeld {
		return Job{}, fmt.Errorf("%w: %s is %s", ErrJobState, id, j.State)
	}
	now := jobTime(a.now())
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state = 'queued', max_attempts = attempts_made + budget, not_before = ?, updated_at = ? WHERE job_id = ?`, now, now, id); err != nil {
		return Job{}, err
	}
	j, err = readJob(ctx, tx, id)
	if err != nil {
		return Job{}, err
	}
	return j, tx.Commit()
}

// ClaimJob leases the oldest due queued job for executor to owner, or
// returns nil when none is due. Before it picks, it reclaims every job of
// that executor whose lease has expired: the interrupted try is recorded as
// lease_expired, and the job goes back to the queue after a backoff, or to
// failed when its attempts are used up.
func (a *Archive) ClaimJob(ctx context.Context, executor, owner string, lease time.Duration) (*Job, error) {
	if owner == "" || lease <= 0 {
		return nil, fmt.Errorf("%w: claim needs an owner and a positive lease", ErrInvalid)
	}
	if err := checkText(owner); err != nil {
		return nil, err
	}
	unlock, err := a.lock(ctx, false)
	if err != nil {
		return nil, err
	}
	defer unlock()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	nowT := a.now()
	now := jobTime(nowT)
	type stale struct {
		id, owner, started string
		made, max          int
	}
	var expired []stale
	rows, err := tx.QueryContext(ctx, `SELECT job_id, lease_owner, started_at, attempts_made, max_attempts FROM jobs WHERE state = 'running' AND executor = ? AND lease_until < ?`, executor, now)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var s stale
		if err := rows.Scan(&s.id, &s.owner, &s.started, &s.made, &s.max); err != nil {
			rows.Close()
			return nil, err
		}
		expired = append(expired, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	for _, s := range expired {
		if _, err := tx.ExecContext(ctx, `INSERT INTO job_attempts (job_id, number, owner, started_at, finished_at, outcome, detail, response, provenance) VALUES (?,?,?,?,?, 'lease_expired', 'the executor did not report before its lease ended', '', '')`, s.id, s.made, s.owner, s.started, now); err != nil {
			return nil, err
		}
		state, notBefore := "queued", jobTime(nowT.Add(jobBackoff(s.made)))
		if s.made >= s.max {
			state, notBefore = JobFailed, now
		}
		if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state = ?, not_before = ?, lease_owner = '', lease_until = '', started_at = '', updated_at = ? WHERE job_id = ?`, state, notBefore, now, s.id); err != nil {
			return nil, err
		}
	}
	var id string
	err = tx.QueryRowContext(ctx, `SELECT job_id FROM jobs WHERE state = 'queued' AND executor = ? AND not_before <= ? ORDER BY created_at, job_id LIMIT 1`, executor, now).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, tx.Commit()
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state = 'running', attempts_made = attempts_made + 1, lease_owner = ?, lease_until = ?, started_at = ?, updated_at = ? WHERE job_id = ?`,
		owner, jobTime(nowT.Add(lease)), now, now, id); err != nil {
		return nil, err
	}
	j, err := readJob(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	return &j, tx.Commit()
}

// CompleteJob records the end of one claimed try. The lease is identified by
// owner and by attempt, the AttemptsMade of the job ClaimJob returned: every
// claim counts an attempt, so a run whose lease was reclaimed cannot finish
// a newer try even when the new claim reuses its owner name. A job that was
// reclaimed or finished returns ErrLeaseLost and records nothing. JobRetry
// requeues with backoff, or fails the job when its attempts are used up.
func (a *Archive) CompleteJob(ctx context.Context, id, owner string, attempt int, out JobOutcome) (Job, error) {
	switch out.State {
	case JobHeld, JobAcquired, JobFailed, JobHuman, JobRetry:
	default:
		return Job{}, fmt.Errorf("%w: outcome %q", ErrInvalid, out.State)
	}
	if (out.State == JobHeld || out.State == JobAcquired) != (out.CaptureID != "") {
		return Job{}, fmt.Errorf("%w: a stored outcome needs a capture, any other outcome must not have one", ErrInvalid)
	}
	unlock, err := a.lock(ctx, false)
	if err != nil {
		return Job{}, err
	}
	defer unlock()
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()
	j, err := readJob(ctx, tx, id)
	if err != nil {
		return Job{}, err
	}
	if j.State != jobStateRunning || j.LeaseOwner != owner || j.AttemptsMade != attempt {
		return Job{}, fmt.Errorf("%w: %s", ErrLeaseLost, id)
	}
	nowT := a.now()
	now := jobTime(nowT)
	state, notBefore, detail := out.State, now, truncateText(out.Detail, 500)
	prov := ""
	if out.CaptureID != "" {
		var obj string
		if err := tx.QueryRowContext(ctx, `SELECT object_id FROM captures WHERE capture_id = ?`, out.CaptureID).Scan(&obj); err != nil {
			return Job{}, fmt.Errorf("%w: %s", ErrNotFound, out.CaptureID)
		}
		prov = ProvRecovered
		switch {
		case j.SeenSHA256 == "":
		case j.SeenSHA256 == obj:
			prov = ProvSeen
		default:
			detail = truncateText(detail+"; the bytes differ from the digest the submitter says the research read", 500)
		}
	}
	if out.State == JobRetry {
		if j.AttemptsMade >= j.MaxAttempts {
			state, detail = JobFailed, truncateText(detail+"; attempts used up", 500)
		} else {
			state, notBefore = jobStateQueued, jobTime(nowT.Add(jobBackoff(j.AttemptsMade)))
		}
	}
	var capID any
	if out.CaptureID != "" {
		capID = out.CaptureID
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO job_attempts (job_id, number, owner, started_at, finished_at, outcome, detail, response, provenance, capture_id) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		id, j.AttemptsMade, owner, j.started, now, out.State, detail, out.Response, prov, capID); err != nil {
		return Job{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE jobs SET state = ?, provenance = ?, not_before = ?, result_capture_id = COALESCE(?, result_capture_id), lease_owner = '', lease_until = '', started_at = '', updated_at = ? WHERE job_id = ?`,
		state, prov, notBefore, capID, now, id); err != nil {
		return Job{}, err
	}
	j, err = readJob(ctx, tx, id)
	if err != nil {
		return Job{}, err
	}
	return j, tx.Commit()
}

const jobSelect = `SELECT job_id, url, executor, submitter, edition_hint, COALESCE(report_capture_id,''), claim_ref, seen_sha256, state, provenance, attempts_made, budget, max_attempts, not_before, lease_owner, lease_until, COALESCE(result_capture_id,''), started_at, created_at, updated_at FROM jobs WHERE job_id = ?`

func readJob(ctx context.Context, q querier, id string) (Job, error) {
	var j Job
	err := q.QueryRowContext(ctx, jobSelect, id).Scan(&j.JobID, &j.URL, &j.Executor, &j.Submitter, &j.EditionHint, &j.ReportCaptureID, &j.ClaimRef, &j.SeenSHA256, &j.State, &j.Provenance, &j.AttemptsMade, &j.budget, &j.MaxAttempts, &j.NotBefore, &j.LeaseOwner, &j.LeaseUntil, &j.ResultCaptureID, &j.started, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, fmt.Errorf("%w: %s", ErrJobNotFound, id)
	}
	if err != nil {
		return Job{}, err
	}
	j.Attempts = []JobAttempt{}
	rows, err := q.QueryContext(ctx, `SELECT number, owner, started_at, finished_at, outcome, detail, response, COALESCE(capture_id,'') FROM job_attempts WHERE job_id = ? ORDER BY attempt_id`, id)
	if err != nil {
		return Job{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var at JobAttempt
		if err := rows.Scan(&at.Number, &at.Owner, &at.StartedAt, &at.FinishedAt, &at.Outcome, &at.Detail, &at.Response, &at.CaptureID); err != nil {
			return Job{}, err
		}
		j.Attempts = append(j.Attempts, at)
	}
	return j, rows.Err()
}

// collections lists the jobs that produced a capture, for its status view.
func (a *Archive) collections(ctx context.Context, captureID string) ([]Collection, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT j.job_id, t.provenance, j.submitter, COALESCE(j.report_capture_id,''), j.claim_ref FROM job_attempts t JOIN jobs j ON j.job_id = t.job_id WHERE t.capture_id = ? ORDER BY t.attempt_id`, captureID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Collection
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.JobID, &c.Provenance, &c.Submitter, &c.ReportCaptureID, &c.ClaimRef); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
