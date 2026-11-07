// Package trialmcp exposes bounded manifest registration, URL submission and
// sanitized status for the isolated no-backup trial, never production MCP.
package trialmcp

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/teslamint/cogvault/internal/archive"
	"github.com/teslamint/cogvault/internal/trial"
	"github.com/teslamint/cogvault/internal/version"
)

const jobIDPattern = `^job_[a-f0-9]{32}$`

var jobIDRE = regexp.MustCompile(jobIDPattern)
var hashRE = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// NewServer registers the entire trial-only tool inventory. It neither starts
// the worker nor fetches a URL. The Service remains the policy authority.
func NewServer(s *trial.Service) *server.MCPServer {
	srv := server.NewMCPServer("cogvault-trial", version.String(), server.WithInstructions("Isolated no-backup trial. Import selected source manifests, submit approved public URLs and read sanitized status. A held capture has unverified completeness. External-original references are not files preserved in this trial. No production wiki, ingest, filesystem or upload access. Request budgets count job attempts, with a separately bounded redirect limit."))
	submit := mcp.NewTool("trial_submit", mcp.WithDescription("Queue one public URL under the operator's media, URL and attempt limits. Optional report_id and source_id must be supplied together; report-linked requests require independent exact URL approval. Does not fetch synchronously."), mcp.WithString("url", mcp.Required(), mcp.MinLength(1), mcp.MaxLength(2048)), mcp.WithString("report_id", mcp.MinLength(1), mcp.MaxLength(96)), mcp.WithString("source_id", mcp.MinLength(1), mcp.MaxLength(48)), mcp.WithReadOnlyHintAnnotation(false), mcp.WithDestructiveHintAnnotation(false))
	submit.InputSchema.AdditionalProperties = false
	status := mcp.NewTool("trial_status", mcp.WithDescription("Read sanitized job status by ID, or current budget usage when job_id is omitted."), mcp.WithString("job_id", mcp.Pattern(jobIDPattern), mcp.MinLength(36), mcp.MaxLength(36)), mcp.WithReadOnlyHintAnnotation(true))
	status.InputSchema.AdditionalProperties = false
	srv.AddTool(submit, func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a, ok := arguments(r, "url", "report_id", "source_id")
		if !ok {
			return failure("invalid_arguments")
		}
		u, ok := a["url"].(string)
		if !ok || len(u) == 0 || len(u) > 2048 {
			return failure("invalid_arguments")
		}
		reportID, hasReport := a["report_id"]
		sourceID, hasSource := a["source_id"]
		if hasReport != hasSource {
			return failure("invalid_arguments")
		}
		var j archive.Job
		var existing bool
		var err error
		if hasReport {
			rid, rok := reportID.(string)
			sid, sok := sourceID.(string)
			if !rok || !sok || len(rid) == 0 || len(rid) > 96 || len(sid) == 0 || len(sid) > 48 {
				return failure("invalid_arguments")
			}
			j, existing, err = s.SubmitSource(ctx, u, "trial-mcp", rid, sid)
		} else {
			j, existing, err = s.Submit(ctx, u, "trial-mcp")
		}
		if err != nil {
			return safeError(err)
		}
		return result(struct {
			Job      jobView `json:"job"`
			Existing bool    `json:"existing"`
		}{viewJob(j), existing})
	})
	srv.AddTool(status, func(ctx context.Context, r mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		a, ok := arguments(r, "job_id")
		if !ok {
			return failure("invalid_arguments")
		}
		raw, present := a["job_id"]
		if !present {
			now := s.Now()
			hour, err := s.A.JobActivity(ctx, now.Add(-time.Hour))
			if err != nil {
				return safeError(err)
			}
			day, err := s.A.JobActivity(ctx, now.Add(-24*time.Hour))
			if err != nil {
				return safeError(err)
			}
			return result(map[string]any{
				"paused": s.Cfg.Paused(), "waiting_or_running": day.Active,
				"requests_last_hour": hour.Fetches, "requests_last_day": day.Fetches,
				"attempts_last_hour": hour.Fetches, "attempts_last_day": day.Fetches,
				"submissions_last_day": day.Created, "budget_unit": "job_attempts",
				"max_http_requests_per_attempt": 1 + s.Cfg.MaxRedirects,
				"attempt_limit_hour":            s.Cfg.MaxFetchesHour, "attempt_limit_day": s.Cfg.MaxFetchesDay,
			})
		}
		id, ok := raw.(string)
		if !ok || !jobIDRE.MatchString(id) {
			return failure("invalid_arguments")
		}
		j, err := s.A.GetJob(ctx, id)
		if err != nil {
			return safeError(err)
		}
		v := struct {
			Job     jobView      `json:"job"`
			Capture *captureView `json:"capture,omitempty"`
		}{Job: viewJob(j)}
		if j.ResultCaptureID != "" {
			c, err := s.A.Status(ctx, j.ResultCaptureID)
			if err != nil {
				return safeError(err)
			}
			v.Capture = viewCapture(c)
		}
		return result(v)
	})
	registerReportTools(srv, s)
	return srv
}

// Arguments are checked again at execution; JSON Schema is not authorization.
func arguments(r mcp.CallToolRequest, keys ...string) (map[string]any, bool) {
	if r.Params.Arguments == nil {
		return map[string]any{}, true
	}
	a, ok := r.Params.Arguments.(map[string]any)
	if !ok {
		return nil, false
	}
	for k := range a {
		allowed := false
		for _, key := range keys {
			if k == key {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, false
		}
	}
	return a, true
}
func result(v any) (*mcp.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return failure("temporarily_unavailable")
	}
	return mcp.NewToolResultText(string(b)), nil
}
func failure(code string) (*mcp.CallToolResult, error) { return mcp.NewToolResultError(code), nil }
func safeError(err error) (*mcp.CallToolResult, error) {
	switch {
	case errors.Is(err, trial.ErrPolicy), errors.Is(err, archive.ErrInvalid):
		return failure("request_refused")
	case errors.Is(err, trial.ErrLimit):
		return failure("limit_reached")
	case errors.Is(err, archive.ErrJobNotFound):
		return failure("job_not_found")
	case errors.Is(err, archive.ErrNotFound):
		return failure("not_found")
	case errors.Is(err, archive.ErrConflict):
		return failure("conflict")
	default:
		return failure("temporarily_unavailable")
	}
}

type jobView struct {
	JobID       string `json:"job_id"`
	State       string `json:"state"`
	Attempts    int    `json:"attempts_made"`
	MaxAttempts int    `json:"max_attempts"`
}

func viewJob(j archive.Job) jobView {
	id := ""
	if jobIDRE.MatchString(j.JobID) {
		id = j.JobID
	}
	state := "unknown"
	switch j.State {
	case "queued", "running", archive.JobHeld, archive.JobAcquired, archive.JobFailed, archive.JobHuman:
		state = j.State
	}
	return jobView{id, state, j.AttemptsMade, j.MaxAttempts}
}

type captureView struct {
	SHA256       string `json:"sha256,omitempty"`
	Size         int64  `json:"size"`
	Completeness string `json:"completeness"`
}

func viewCapture(c archive.Capture) *captureView {
	hash := ""
	if hashRE.MatchString(c.ObjectID) {
		hash = c.ObjectID
	}
	completeness := "unknown"
	switch c.Completeness {
	case "verified", "incomplete", "unknown":
		completeness = c.Completeness
	}
	return &captureView{hash, c.Size, completeness}
}
