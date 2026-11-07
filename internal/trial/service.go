package trial

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/teslamint/cogvault/internal/archive"
)

// ErrLimit is a submission refused because a trial limit is reached.
var ErrLimit = errors.New("trial limit reached")

// ErrPolicy is a URL refused by the trial policy.
var ErrPolicy = errors.New("refused by the trial policy")

const maxURLLength = 2048

// Service runs the trial queue on its own archive root.
type Service struct {
	Cfg Config
	A   *archive.Archive
	// Now and Acquirer are replaceable for tests. The Acquirer is a base: Run
	// adds the allowlist, the PDF-only rule, and the size bound to it.
	Now      func() time.Time
	Acquirer archive.Acquirer
	Owner    string
	Lease    time.Duration
	Marker   Marker

	// relaxed lets this package's tests use a plain-http local server: it drops
	// the https-only and default-port rules and nothing else. No setting reaches it.
	relaxed bool
}

// Open checks the root, initializes it when it is new or empty, and opens the
// archive in it. Nothing outside the root is touched.
func Open(cfg Config) (*Service, error) {
	s := &Service{Cfg: cfg, Now: time.Now, Lease: archive.DefaultJobLease}
	m, err := cfg.InitRoot(s.Now(), newTrialID)
	if err != nil {
		return nil, err
	}
	s.Marker = m
	a, err := archive.Open(cfg.Root)
	if err != nil {
		return nil, fmt.Errorf("the trial archive could not be opened")
	}
	s.A = a
	return s, nil
}

// Close closes the archive.
func (s *Service) Close() error {
	if s.A == nil {
		return nil
	}
	return s.A.Close()
}

func newTrialID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "trial_" + hex.EncodeToString(b[:])
}

// checkURL applies the trial URL policy: https, no userinfo or non-default port,
// a bounded length, and an exact allowlisted host. The archive's own checks (no
// fragment or query, except its one audited source) run again at submit and at
// fetch.
func (s *Service) checkURL(raw string) error {
	if len(raw) > maxURLLength {
		return fmt.Errorf("%w: the URL is too long", ErrPolicy)
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%w: not a URL", ErrPolicy)
	}
	if u.Scheme != "https" && !(s.relaxed && u.Scheme == "http") {
		return fmt.Errorf("%w: only https is fetched", ErrPolicy)
	}
	if u.User != nil || (!s.relaxed && u.Port() != "" && u.Port() != "443") {
		return fmt.Errorf("%w: credentials and ports are not accepted", ErrPolicy)
	}
	if !s.Cfg.HostAllowed(u.Hostname()) {
		return fmt.Errorf("%w: the host is not on the allowed list", ErrPolicy)
	}
	return nil
}

// Submit queues one public PDF URL. It refuses a URL outside the policy, and a
// submission while the queue is full or the daily submission limit is reached
// (a repeated URL counts too, which is the cautious side). It does not fetch.
func (s *Service) Submit(ctx context.Context, rawURL, label string) (archive.Job, bool, error) {
	if err := s.checkURL(rawURL); err != nil {
		return archive.Job{}, false, err
	}
	unlock, err := s.claimLock(ctx)
	if err != nil {
		return archive.Job{}, false, err
	}
	defer unlock()
	now := s.Now()
	day, err := s.A.JobActivity(ctx, now.Add(-24*time.Hour))
	if err != nil {
		return archive.Job{}, false, err
	}
	if day.Active >= s.Cfg.MaxQueued {
		return archive.Job{}, false, fmt.Errorf("%w: %d jobs are already waiting or running", ErrLimit, day.Active)
	}
	if day.Created >= s.Cfg.MaxSubmitsPerDay {
		return archive.Job{}, false, fmt.Errorf("%w: %d submissions in the last 24 hours", ErrLimit, day.Created)
	}
	if label == "" {
		label = "trial"
	}
	return s.A.SubmitJob(ctx, archive.JobRequest{
		URL: rawURL, Submitter: label, Executor: archive.ExecutorHTTP, MaxAttempts: s.Cfg.MaxAttempts,
	})
}

// acquirer is the base Acquirer with the trial policy applied.
func (s *Service) acquirer() archive.Acquirer {
	q := s.Acquirer
	q.AllowHost = s.Cfg.HostAllowed
	q.PDFOnly = true
	q.MaxBytes = s.Cfg.MaxBytes
	return q
}

// Wait is why RunOnce did not run a job.
type Wait struct{ Reason string }

// budget reports why no request may be started now, or "".
func (s *Service) budget(ctx context.Context) (string, error) {
	now := s.Now()
	hour, err := s.A.JobActivity(ctx, now.Add(-time.Hour))
	if err != nil {
		return "", err
	}
	day, err := s.A.JobActivity(ctx, now.Add(-24*time.Hour))
	if err != nil {
		return "", err
	}
	switch {
	case hour.Fetches >= s.Cfg.MaxFetchesHour:
		return "the hourly request limit is reached", nil
	case day.Fetches >= s.Cfg.MaxFetchesDay:
		return "the daily request limit is reached", nil
	case !day.LastStart.IsZero() && now.Sub(day.LastStart) < s.Cfg.MinInterval:
		return "waiting out the minimum interval between requests", nil
	}
	return "", nil
}

// RunOnce starts at most one job. The worker's checks come first and do not
// depend on how a job was queued: the request budget is checked before the
// claim, and the claimed job is checked against the allowlist, the attempt cap,
// and the scheme before a single byte is sent. A job that fails the policy is
// ended as failed without a request. It returns the finished job, or nil with a
// reason when nothing ran.
func (s *Service) RunOnce(ctx context.Context) (*archive.Job, string, error) {
	if s.Cfg.Paused() {
		return nil, "paused", nil
	}
	job, why, err := s.claimWithinBudget(ctx)
	if err != nil || job == nil {
		return nil, why, err
	}
	if why := s.refuse(job); why != "" {
		done, cerr := s.A.CompleteJob(ctx, job.JobID, s.Owner, job.AttemptsMade, archive.JobOutcome{
			State: archive.JobFailed, Detail: "trial policy: " + why + "; nothing was sent",
		})
		if cerr != nil {
			return job, "", cerr
		}
		return &done, "", nil
	}
	done, err := s.A.RunClaimedJob(ctx, s.acquirer(), job, s.Owner)
	return done, "", err
}

// claimWithinBudget holds the cross-process lock until ClaimJob records its
// attempt, so a second worker sees that attempt in its budget query.
func (s *Service) claimWithinBudget(ctx context.Context) (*archive.Job, string, error) {
	unlock, err := s.claimLock(ctx)
	if err != nil {
		return nil, "", err
	}
	defer unlock()
	if s.Cfg.Paused() {
		return nil, "paused", nil
	}
	if why, err := s.budget(ctx); err != nil || why != "" {
		return nil, why, err
	}
	job, err := s.A.ClaimJob(ctx, archive.ExecutorHTTP, s.Owner, s.Lease)
	return job, "", err
}

// refuse returns why a claimed job may not be fetched, or "".
func (s *Service) refuse(job *archive.Job) string {
	switch {
	case job.Executor != archive.ExecutorHTTP:
		return "only the http executor is run"
	case s.checkURL(job.URL) != nil:
		return "the URL is outside the allowed hosts or scheme"
	case job.AttemptsMade > s.Cfg.MaxAttempts:
		return "the trial attempt limit is used up"
	}
	return ""
}

// Allowed is the worker loop's gate: the root must still pass its checks and the
// pause file must be absent. It never consults the ADR 0027 activation check.
func (s *Service) Allowed() error {
	if err := s.Cfg.CheckRoot(); err != nil {
		return err
	}
	if s.Cfg.Paused() {
		return errors.New("trial paused: the PAUSE file exists in the trial root")
	}
	return nil
}

// Summary is a short, secret-free description of the policy in force.
func (s *Service) Summary() string {
	return fmt.Sprintf("trial %s: hosts=%s max_queued=%d submits/day=%d requests/hour=%d requests/day=%d interval=%s attempts=%d max_bytes=%d",
		s.Marker.ID, strings.Join(s.Cfg.AllowedHosts, ","), s.Cfg.MaxQueued, s.Cfg.MaxSubmitsPerDay,
		s.Cfg.MaxFetchesHour, s.Cfg.MaxFetchesDay, s.Cfg.MinInterval, s.Cfg.MaxAttempts, s.Cfg.MaxBytes)
}
