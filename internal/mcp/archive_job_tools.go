package mcp

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/teslamint/cogvault/internal/archive"
)

// Collection job tools (SPEC §9.16 "Collection jobs"). They are archive
// tools: they exist only while capture is enabled, pass the same gate and
// the server-wide auth, and are removed on a disable reload. They queue a
// public URL for the archive host's worker and read the job back by ID, so
// a different chat client can pick the job up later. They never fetch in the
// request, take no host path, and cannot delete or overwrite anything.

func archiveJobSubmitTool() mcp.Tool {
	return mcp.NewTool("archive_job_submit",
		mcp.WithDescription("Queue one public http(s) URL for the archive host's collector and return a job_id. The collector runs later, outside this chat, and stores the bytes as a held capture with unknown completeness; read the result with archive_job_status. URLs with credentials, a query, or a fragment are refused. The collector never logs in: a login, paywall, or challenge ends the job as human_action_required. Repeating the same request returns the same job. submitter is a label, not an identity."),
		mcp.WithString("url", mcp.Required(), mcp.Description("Absolute public http(s) URL; no userinfo, query, or fragment")),
		mcp.WithString("submitter", mcp.Required(), mcp.Description("Label for who is asking (client or session name)")),
		mcp.WithString("edition", mcp.Description("Edition wanted (volume, version, year); recorded only")),
		mcp.WithString("report_capture_id", mcp.Description("Capture ID of the imported research report that cited this URL")),
		mcp.WithString("claim", mcp.Description("Claim or locator the source is meant to support")),
		mcp.WithString("seen_sha256", mcp.Description("sha256:<64 hex> of the bytes the research read, if you have them; a match is recorded as seen_during_research, otherwise the bytes are recovered_after_research")),
		mcp.WithString("executor", mcp.Description("http (default) or browser (an interactive executor that is not part of this server)")),
		mcp.WithString("key", mcp.Description("Optional idempotency key")),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(true),
	)
}

func archiveJobStatusTool() mcp.Tool {
	return mcp.NewTool("archive_job_status",
		mcp.WithDescription("Show one collection job: state (queued, running, held, acquired, failed, human_action_required), every attempt with its outcome and reason, and the resulting capture_id. A held or acquired job names the capture; read it with archive_status. Works from any client that has the job_id."),
		mcp.WithString("job_id", mcp.Required(), mcp.Description("Job ID (job_...)")),
		mcp.WithReadOnlyHintAnnotation(true),
	)
}

func archiveJobRetryTool() mcp.Tool {
	return mcp.NewTool("archive_job_retry",
		mcp.WithDescription("Queue a failed, human_action_required, or held job again with a fresh attempt budget. History is kept. The collector uses no credentials, so retrying an access-restricted URL fails the same way unless access changed."),
		mcp.WithString("job_id", mcp.Required(), mcp.Description("Job ID (job_...)")),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(false),
	)
}

// archiveJobView is the remote projection of a job. It omits the worker
// name (host and PID) and each attempt's acquisition record (checked
// addresses, response headers): a remote client gets state and reasons, not
// facts about the archive host's network.
type archiveJobView struct {
	JobID           string                  `json:"job_id"`
	URL             string                  `json:"url"`
	Executor        string                  `json:"executor"`
	Submitter       string                  `json:"submitter"`
	EditionHint     string                  `json:"edition_hint,omitempty"`
	ReportCaptureID string                  `json:"report_capture_id,omitempty"`
	ClaimRef        string                  `json:"claim_ref,omitempty"`
	SeenSHA256      string                  `json:"seen_sha256,omitempty"`
	State           string                  `json:"state"`
	Provenance      string                  `json:"provenance,omitempty"`
	AttemptsMade    int                     `json:"attempts_made"`
	MaxAttempts     int                     `json:"max_attempts"`
	NotBefore       string                  `json:"not_before"`
	ResultCaptureID string                  `json:"result_capture_id,omitempty"`
	CreatedAt       string                  `json:"created_at"`
	UpdatedAt       string                  `json:"updated_at"`
	Attempts        []archiveJobAttemptView `json:"attempts"`
}

type archiveJobAttemptView struct {
	Number     int    `json:"number"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Outcome    string `json:"outcome"`
	Detail     string `json:"detail"`
	CaptureID  string `json:"capture_id,omitempty"`
}

func jobView(j archive.Job) archiveJobView {
	v := archiveJobView{
		JobID: j.JobID, URL: j.URL, Executor: j.Executor, Submitter: j.Submitter, EditionHint: j.EditionHint,
		ReportCaptureID: j.ReportCaptureID, ClaimRef: j.ClaimRef, SeenSHA256: j.SeenSHA256, State: j.State,
		Provenance: j.Provenance, AttemptsMade: j.AttemptsMade, MaxAttempts: j.MaxAttempts, NotBefore: j.NotBefore,
		ResultCaptureID: j.ResultCaptureID, CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt,
		Attempts: make([]archiveJobAttemptView, 0, len(j.Attempts)),
	}
	for _, at := range j.Attempts {
		v.Attempts = append(v.Attempts, archiveJobAttemptView{
			Number: at.Number, StartedAt: at.StartedAt, FinishedAt: at.FinishedAt, Outcome: at.Outcome,
			Detail: at.Detail, CaptureID: at.CaptureID,
		})
	}
	return v
}

func handleArchiveJobSubmit(a *archive.Archive) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		url, err := req.RequireString("url")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: url"), nil
		}
		submitter, err := req.RequireString("submitter")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: submitter"), nil
		}
		j, existing, err := a.SubmitJob(ctx, archive.JobRequest{
			URL: url, Submitter: submitter, Key: req.GetString("key", ""),
			EditionHint: req.GetString("edition", ""), ReportCaptureID: req.GetString("report_capture_id", ""),
			ClaimRef: req.GetString("claim", ""), SeenSHA256: req.GetString("seen_sha256", ""),
			Executor: req.GetString("executor", ""),
		})
		if err != nil {
			return archiveError(err), nil
		}
		return mcp.NewToolResultJSON(struct {
			Job      archiveJobView `json:"job"`
			Existing bool           `json:"existing"`
		}{jobView(j), existing})
	}
}

func handleArchiveJobStatus(a *archive.Archive) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := req.RequireString("job_id")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: job_id"), nil
		}
		j, err := a.GetJob(ctx, id)
		if err != nil {
			return archiveError(err), nil
		}
		return mcp.NewToolResultJSON(jobView(j))
	}
}

func handleArchiveJobRetry(a *archive.Archive) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		id, err := req.RequireString("job_id")
		if err != nil {
			return mcp.NewToolResultError("missing required parameter: job_id"), nil
		}
		j, err := a.RetryJob(ctx, id)
		if err != nil {
			return archiveError(err), nil
		}
		return mcp.NewToolResultJSON(jobView(j))
	}
}
