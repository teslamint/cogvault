package archive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// The public HTTP executor of the collection queue. It runs one claimed job
// through Archive.acquire, so it has the same defenses as `archive acquire`:
// http(s) only, no credentials, query, or fragment on any hop, every DNS
// answer public, the connection to the checked address, bounded redirects,
// time, and size, no cookies, proxy, or compression. It never logs in or
// retries with credentials: a 401, 403, 407, or 451 ends the job as
// human_action_required.

// DefaultJobLease is how long a claim holds a job. A download is bounded by
// AcquireTimeout, so the lease comfortably outlasts one honest try.
const DefaultJobLease = 5 * time.Minute

// RunNextJob claims one due http job, runs it, and records the result. It
// returns nil when no job is due. When ctx ends mid-download the try is not
// recorded: the lease expires and the next claim reclaims the job, so a
// stopped worker never reports a result it did not finish.
func (a *Archive) RunNextJob(ctx context.Context, q Acquirer, owner string, lease time.Duration) (*Job, error) {
	job, err := a.ClaimJob(ctx, ExecutorHTTP, owner, lease)
	if err != nil || job == nil {
		return nil, err
	}
	return a.RunClaimedJob(ctx, q, job, owner)
}

// RunClaimedJob runs a job this worker has already claimed, which lets a caller
// inspect the job (its URL, executor, attempt budget) between the claim and the
// first request, and refuse it with CompleteJob before anything is sent.
func (a *Archive) RunClaimedJob(ctx context.Context, q Acquirer, job *Job, owner string) (*Job, error) {
	// The capture and the job's completion are written in one transaction,
	// behind the lease check: a try whose lease was reclaimed while it
	// downloaded stores nothing and returns ErrLeaseLost.
	bind := &jobBinding{id: job.JobID, owner: owner, attempt: job.AttemptsMade, outcome: func(res Result, rec AcquisitionRecord) JobOutcome {
		return classifyAcquire(res, rec, nil)
	}}
	res, rec, aerr := a.acquire(ctx, q, job.URL, SourceRequest{Supplier: JobSupplierPrefix + job.JobID, bind: bind})
	// done is set only after the transaction committed; an error means it did not.
	if aerr == nil && bind.done != nil {
		return bind.done, nil
	}
	if ctx.Err() != nil {
		return job, ctx.Err()
	}
	if errors.Is(aerr, ErrLeaseLost) || errors.Is(aerr, ErrJobNotFound) {
		return job, aerr
	}
	out := classifyAcquire(res, rec, aerr)
	done, err := a.CompleteJob(ctx, job.JobID, owner, job.AttemptsMade, out)
	if err != nil {
		return job, err
	}
	return &done, nil
}

// classifyAcquire maps an acquisition result to a job outcome. Refusals
// that a later try cannot change (blocked address, bad URL, oversize, a 4xx
// other than the transient ones, a deleted or damaged object) fail the job
// at once. Network errors, timeouts, 408/425/429/5xx, and a short or broken
// body are retried with backoff until the attempts are used up.
func classifyAcquire(res Result, rec AcquisitionRecord, err error) JobOutcome {
	out := JobOutcome{}
	if b, merr := json.Marshal(rec); merr == nil {
		out.Response = string(b)
	}
	if err == nil {
		// Acquire never judges completeness: a download alone does not show
		// the document is whole, so the bytes are held, not handed off.
		out.State, out.CaptureID = JobHeld, res.CaptureID
		out.Detail = "stored; completeness unknown, so the capture is held from hand-off"
		if res.Existing {
			out.Detail = "stored as an existing capture of the same request; completeness unknown, held from hand-off"
		}
		return out
	}
	var status *RemoteStatusError
	switch {
	case errors.Is(err, ErrAccessRestricted):
		out.State = JobHuman
		out.Detail = fmt.Sprintf("access restricted (HTTP %d or similar); a person must obtain an authorized copy, the collector never uses credentials", accessStatus(rec))
	case errors.Is(err, ErrBlockedAddress):
		out.State, out.Detail = JobFailed, "destination is not a public address"
	case errors.Is(err, ErrHostNotAllowed):
		out.State, out.Detail = JobFailed, "a host is not on the allowed list (the first URL or a redirect); nothing was sent to it"
	case errors.Is(err, ErrNotPDF):
		out.State, out.Detail = JobFailed, "the response is not a PDF"
	case errors.Is(err, ErrInvalid):
		out.State, out.Detail = JobFailed, "the URL or a redirect was refused (scheme, credentials, query, or fragment)"
	case errors.Is(err, ErrTooLarge):
		out.State, out.Detail = JobFailed, fmt.Sprintf("the response is larger than the %d byte limit", MaxObjectBytes)
	case errors.Is(err, ErrDeleted):
		out.State, out.Detail = JobFailed, "the bytes are quarantined or purged; an operator must restore them first"
	case errors.Is(err, ErrIntegrity):
		out.State, out.Detail = JobFailed, "a stored object failed its integrity check; an operator must check the archive"
	case errors.Is(err, ErrStore):
		// The download worked; storing it did not. Retrying would download the
		// file again for the same fault, so the job fails and an operator checks
		// the archive and retries it. Only a fixed code is recorded: the error
		// text can name host paths or registry details, and job status is
		// readable by remote clients.
		out.State, out.Detail = JobFailed, "storage_failed ("+storeFaultCode(err)+"): the file was downloaded but could not be stored; an operator must check the archive host (disk space, permissions, registry) and retry the job"
	case errors.As(err, &status):
		out.Detail = fmt.Sprintf("remote refused or unusable response: HTTP %d", status.Status)
		switch status.Status {
		case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests:
			out.State = JobRetry
		default:
			if status.Status >= 500 {
				out.State = JobRetry
			} else {
				out.State = JobFailed
			}
		}
	case errors.Is(err, ErrRemote):
		out.State, out.Detail = JobRetry, "the remote response was unusable (redirect limit, broken connection, or length mismatch)"
	default:
		out.State, out.Detail = JobRetry, networkReason(err)
	}
	return out
}

// networkReason names a transport failure without its text. A Go network
// error carries resolver and socket addresses of the archive host, which a
// remote client reading job status must not learn.
func networkReason(err error) string {
	var dns *net.DNSError
	var ne net.Error
	switch {
	case errors.As(err, &dns):
		return "network error: DNS lookup failed"
	case errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &ne) && ne.Timeout()):
		return "network error: timed out"
	default:
		return "network error: could not connect or the connection broke"
	}
}

// accessStatus is the status of the last hop, or 0 when none was recorded.
func accessStatus(rec AcquisitionRecord) int {
	if len(rec.Hops) == 0 {
		return 0
	}
	return rec.Hops[len(rec.Hops)-1].Status
}
