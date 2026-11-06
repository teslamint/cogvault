package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/teslamint/cogvault/internal/archive"
	"github.com/teslamint/cogvault/internal/config"
)

// newArchiveJobCmd is the durable collection queue (SPEC §9.16 "Collection
// jobs"). Submitting and reading a job are separate from running it: any
// process on the archive host can submit or read by job ID, and `job run` is
// the long-lived worker that resumes queued and lease-expired jobs after a
// restart. The worker uses only public HTTP; it has no login, cookie, or
// credential option.
func newArchiveJobCmd() *cobra.Command {
	job := &cobra.Command{
		Use:   "job",
		Short: "Durable source collection queue: submit URLs, read their status, run the worker",
	}

	submit := &cobra.Command{
		Use:   "submit <url>",
		Short: "Queue one public URL for collection and print its job ID (idempotent)",
		Args:  cobra.ExactArgs(1),
		RunE:  runArchiveJobSubmit,
	}
	submit.Flags().String("submitter", "", "who is asking, for the record (required)")
	submit.Flags().String("edition", "", "edition wanted (volume, version, year); recorded, never used to choose")
	submit.Flags().String("report", "", "report capture ID that cited this URL")
	submit.Flags().String("claim", "", "claim or locator the source is meant to support")
	submit.Flags().String("seen-sha256", "", "sha256:<hex> of the bytes the research read; a match is recorded as seen_during_research")
	submit.Flags().String("executor", archive.ExecutorHTTP, "http (this worker) or browser (a separate interactive executor)")
	submit.Flags().String("key", "", "idempotency key (default: derived from URL, edition, report, claim)")
	submit.Flags().Int("max-attempts", 0, "tries before the job fails (default 5)")
	submit.Flags().Bool("json", false, "output as JSON")
	_ = submit.MarkFlagRequired("submitter")

	status := &cobra.Command{
		Use:   "status <job-id>",
		Short: "Show one job with every attempt",
		Args:  cobra.ExactArgs(1),
		RunE:  runArchiveJobStatus,
	}
	status.Flags().Bool("json", false, "output as JSON")

	retry := &cobra.Command{
		Use:   "retry <job-id>",
		Short: "Queue a failed, human_action_required, or held job again with a fresh attempt budget",
		Args:  cobra.ExactArgs(1),
		RunE:  runArchiveJobRetry,
	}
	retry.Flags().Bool("json", false, "output as JSON")

	run := &cobra.Command{
		Use:   "run",
		Short: "Worker: run due http jobs until interrupted (or once with --once)",
		Args:  cobra.NoArgs,
		RunE:  runArchiveJobRun,
	}
	run.Flags().Bool("once", false, "run due jobs until none is due, then exit")
	run.Flags().String("owner", "", "worker name recorded on each attempt (default: host and PID)")
	run.Flags().Duration("lease", archive.DefaultJobLease, "how long one claim holds a job")
	run.Flags().Duration("poll", 30*time.Second, "wait between checks when no job is due")

	job.AddCommand(submit, status, retry, run)
	return job
}

func writeJobJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printJob(w io.Writer, j archive.Job) {
	fmt.Fprintf(w, "job %s: %s\n", j.JobID, j.State)
	fmt.Fprintf(w, "  url: %s (executor %s, submitted by %s)\n", j.URL, j.Executor, j.Submitter)
	if j.EditionHint != "" {
		fmt.Fprintf(w, "  edition wanted: %s\n", j.EditionHint)
	}
	if j.ReportCaptureID != "" || j.ClaimRef != "" {
		fmt.Fprintf(w, "  report: %s claim: %s\n", j.ReportCaptureID, j.ClaimRef)
	}
	fmt.Fprintf(w, "  attempts: %d of %d; created %s; updated %s\n", j.AttemptsMade, j.MaxAttempts, j.CreatedAt, j.UpdatedAt)
	switch j.State {
	case "queued":
		fmt.Fprintf(w, "  not before: %s\n", j.NotBefore)
	case "running":
		fmt.Fprintf(w, "  leased to %s until %s\n", j.LeaseOwner, j.LeaseUntil)
	}
	if j.ResultCaptureID != "" {
		fmt.Fprintf(w, "  capture: %s (%s)\n", j.ResultCaptureID, j.Provenance)
	}
	for _, at := range j.Attempts {
		fmt.Fprintf(w, "  attempt %d by %s: %s at %s: %s\n", at.Number, at.Owner, at.Outcome, at.FinishedAt, at.Detail)
	}
}

func outputJob(cmd *cobra.Command, j archive.Job) error {
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		return writeJobJSON(cmd.OutOrStdout(), j)
	}
	printJob(cmd.OutOrStdout(), j)
	return nil
}

func runArchiveJobSubmit(cmd *cobra.Command, args []string) error {
	a, err := openArchive(cmd)
	if err != nil {
		return err
	}
	defer a.Close()
	f := cmd.Flags()
	var req archive.JobRequest
	req.URL = args[0]
	req.Submitter, _ = f.GetString("submitter")
	req.EditionHint, _ = f.GetString("edition")
	req.ReportCaptureID, _ = f.GetString("report")
	req.ClaimRef, _ = f.GetString("claim")
	req.SeenSHA256, _ = f.GetString("seen-sha256")
	req.Executor, _ = f.GetString("executor")
	req.Key, _ = f.GetString("key")
	req.MaxAttempts, _ = f.GetInt("max-attempts")
	j, existing, err := a.SubmitJob(cmd.Context(), req)
	if err != nil {
		return err
	}
	if existing {
		fmt.Fprintln(cmd.ErrOrStderr(), "existing job returned; nothing changed")
	}
	return outputJob(cmd, j)
}

func runArchiveJobStatus(cmd *cobra.Command, args []string) error {
	a, err := openArchive(cmd)
	if err != nil {
		return err
	}
	defer a.Close()
	j, err := a.GetJob(cmd.Context(), args[0])
	if err != nil {
		return err
	}
	return outputJob(cmd, j)
}

func runArchiveJobRetry(cmd *cobra.Command, args []string) error {
	a, err := openArchive(cmd)
	if err != nil {
		return err
	}
	defer a.Close()
	j, err := a.RetryJob(cmd.Context(), args[0])
	if err != nil {
		return err
	}
	return outputJob(cmd, j)
}

// workerAllowed is the worker's activation gate, the same one `serve` uses
// for the capture tools (ADR 0027 item 14): the config must name this root,
// capture must be enabled, and the production activation check must pass.
// Flipping archive.capture_enabled off in the config therefore pauses the
// worker as well as the MCP tools. The error carries a code and key names
// only. activation is injected so a test can use its own check; the command
// passes productionActivation.
func workerAllowed(configPath, root string, activation func(*config.Config) error) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("collector blocked: config could not be loaded: %w", err)
	}
	if cfg.Archive.Root == "" || filepath.Clean(cfg.Archive.Root) != filepath.Clean(root) {
		return &activationError{code: "worker_root_not_the_configured_archive_root", keys: []string{"archive.root"}}
	}
	if !cfg.Archive.CaptureEnabled {
		return &activationError{code: "capture_disabled", keys: []string{"archive.capture_enabled"}}
	}
	return activation(cfg)
}

// workerOptions are the worker loop's settings.
type workerOptions struct {
	owner    string
	lease    time.Duration
	poll     time.Duration
	once     bool
	acquirer archive.Acquirer
}

// runWorker is the worker loop, separated from the command so a package test
// can drive it on a temporary root with an injected gate. The command always
// passes the real activation gate; there is no flag or setting that skips it.
//
// allowed is checked before the archive is opened and before every claim, and
// the loop claims nothing while it returns an error. With once, a closed gate
// is an error returned before anything is opened. A long-running worker
// instead pauses and logs once per change: with a restart policy an exit
// would be a crash loop, and flipping archive.capture_enabled on is how an
// operator resumes it. The archive is opened only after the gate first
// passes, so a gated worker creates nothing under its root. ctx ends the
// loop: a download in progress is abandoned without a record and its lease
// expiry lets the next worker resume the job.
func runWorker(ctx context.Context, open func() (*archive.Archive, error), allowed func() error, o workerOptions, out, errOut io.Writer) error {
	var a *archive.Archive
	defer func() {
		if a != nil {
			a.Close()
		}
	}()
	lastBlocked := ""
	for {
		if err := allowed(); err != nil {
			if o.once {
				return err
			}
			if msg := err.Error(); msg != lastBlocked {
				fmt.Fprintf(errOut, "paused: %s\n", msg)
				lastBlocked = msg
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(o.poll):
			}
			continue
		}
		if lastBlocked != "" {
			fmt.Fprintln(errOut, "resumed")
			lastBlocked = ""
		}
		if a == nil {
			var err error
			if a, err = open(); err != nil {
				return err
			}
		}
		j, err := a.RunNextJob(ctx, o.acquirer, o.owner, o.lease)
		switch {
		case ctx.Err() != nil:
			return nil
		case errors.Is(err, archive.ErrLeaseLost):
			// Another worker reclaimed the job; its try is the one that counts.
			fmt.Fprintf(errOut, "job %s: %v\n", j.JobID, err)
		case err != nil:
			fmt.Fprintf(errOut, "worker error: %v\n", err)
			if o.once {
				return err
			}
		case j != nil:
			fmt.Fprintf(out, "job %s: %s (attempt %d of %d)\n", j.JobID, j.State, j.AttemptsMade, j.MaxAttempts)
			continue
		case o.once:
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(o.poll):
		}
	}
}

// runArchiveJobRun starts the worker behind the unconditional activation
// gate (workerAllowed with productionActivation). It needs an explicit
// --config; it has no option to run without one.
func runArchiveJobRun(cmd *cobra.Command, _ []string) error {
	f := cmd.Flags()
	if c := cmd.Flag("config"); c == nil || !c.Changed {
		return fmt.Errorf("job run needs an explicit --config: the worker runs only behind the ADR 0027 activation gate")
	}
	rootFlag, _ := f.GetString("root")
	root, err := filepath.Abs(rootFlag)
	if err != nil {
		return err
	}
	configPath, err := resolveConfigPath(cmd)
	if err != nil {
		return err
	}
	var o workerOptions
	o.once, _ = f.GetBool("once")
	o.owner, _ = f.GetString("owner")
	o.lease, _ = f.GetDuration("lease")
	o.poll, _ = f.GetDuration("poll")
	if o.owner == "" {
		host, _ := os.Hostname()
		o.owner = fmt.Sprintf("%s-%d", host, os.Getpid())
	}
	if o.poll <= 0 {
		return fmt.Errorf("--poll must be positive")
	}
	ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runWorker(ctx,
		func() (*archive.Archive, error) { return openArchive(cmd) },
		func() error { return workerAllowed(configPath, root, productionActivation) },
		o, cmd.OutOrStdout(), cmd.ErrOrStderr())
}
