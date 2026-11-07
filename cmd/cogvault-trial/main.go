// Command cogvault-trial is the no-backup public-PDF trial of the collection
// queue (ADR 0028). It is not the production collector: it has its own root and
// registry, takes its whole policy from COGVAULT_TRIAL_* environment variables
// (so the worker and a command run with `docker exec` in the same container
// read one policy), fetches only https PDFs from an exact-host allowlist within
// fixed request limits, and never links the ingest pipeline, the MCP server, the
// wiki, or the ADR 0027 production activation gate.
//
//	cogvault-trial check              validate the policy and the root (creates nothing)
//	cogvault-trial submit URL         queue one public PDF
//	cogvault-trial status [JOB_ID]    one job, or the request budget in use
//	cogvault-trial run [--once]       the worker
//	cogvault-trial pause | resume     create or remove the PAUSE file in the root
//	cogvault-trial version
//
// There is no delete or cleanup command: removing a trial root is a manual step
// (docs/deployment/trial.md).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/teslamint/cogvault/internal/archive"
	"github.com/teslamint/cogvault/internal/trial"
	"github.com/teslamint/cogvault/internal/version"
)

func main() {
	if err := run(os.Args[1:], os.Environ(), os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "cogvault-trial:", err)
		os.Exit(1)
	}
}

func run(args, environ []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return errors.New("a command is required: check, submit, status, run, pause, resume, version")
	}
	cmd, rest := args[0], args[1:]
	if cmd == "version" {
		fmt.Fprintln(out, version.String())
		return nil
	}
	if cmd == "-h" || cmd == "--help" || cmd == "help" {
		fmt.Fprintln(out, "usage: cogvault-trial check | submit URL | status [JOB_ID] | run [--once] | pause | resume | version")
		return nil
	}
	cfg, err := trial.FromEnviron(environ)
	if err != nil {
		return err
	}
	switch cmd {
	case "check":
		if err := cfg.CheckRoot(); err != nil {
			return err
		}
		fmt.Fprintf(out, "ok: root %s, hosts %v, no ADR 0027 gate is used and no backup is assumed\n", cfg.Root, cfg.AllowedHosts)
		return nil
	case "pause", "resume":
		if err := cfg.CheckRoot(); err != nil {
			return err
		}
		if err := cfg.SetPaused(cmd == "pause"); err != nil {
			return fmt.Errorf("the pause file could not be changed")
		}
		fmt.Fprintf(out, "%sd\n", cmd)
		return nil
	case "submit":
		return submit(cfg, rest, out)
	case "status":
		return status(cfg, rest, out)
	case "run":
		return worker(cfg, rest, out, errOut)
	}
	return fmt.Errorf("unknown command %q", cmd)
}

func open(cfg trial.Config) (*trial.Service, error) {
	s, err := trial.Open(cfg)
	if err != nil {
		return nil, err
	}
	host, _ := os.Hostname()
	s.Owner = fmt.Sprintf("trial-%s-%d", host, os.Getpid())
	return s, nil
}

func submit(cfg trial.Config, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("submit", flag.ContinueOnError)
	label := fs.String("label", "", "who or what queued it (free text)")
	asJSON := fs.Bool("json", false, "print the job as JSON")
	if err := fs.Parse(args); err != nil {
		return orHelp(err)
	}
	if fs.NArg() != 1 {
		return errors.New("submit takes exactly one URL")
	}
	s, err := open(cfg)
	if err != nil {
		return err
	}
	defer s.Close()
	j, existing, err := s.Submit(context.Background(), fs.Arg(0), *label)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(out, map[string]any{"job": j, "existing": existing})
	}
	verb := "queued"
	if existing {
		verb = "already queued"
	}
	fmt.Fprintf(out, "%s %s: %s (attempts %d of %d)\n", verb, j.JobID, j.State, j.AttemptsMade, j.MaxAttempts)
	return nil
}

func status(cfg trial.Config, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print as JSON")
	if err := fs.Parse(args); err != nil {
		return orHelp(err)
	}
	s, err := open(cfg)
	if err != nil {
		return err
	}
	defer s.Close()
	ctx := context.Background()
	if fs.NArg() == 0 {
		now := time.Now()
		hour, err := s.A.JobActivity(ctx, now.Add(-time.Hour))
		if err != nil {
			return err
		}
		day, err := s.A.JobActivity(ctx, now.Add(-24*time.Hour))
		if err != nil {
			return err
		}
		if *asJSON {
			return printJSON(out, map[string]any{"trial_id": s.Marker.ID, "paused": cfg.Paused(), "waiting_or_running": day.Active,
				"requests_last_hour": hour.Fetches, "requests_last_day": day.Fetches, "submissions_last_day": day.Created})
		}
		fmt.Fprintln(out, s.Summary())
		fmt.Fprintf(out, "in use: waiting or running %d/%d, requests %d/%d this hour and %d/%d today, submissions %d/%d today, paused=%v\n",
			day.Active, cfg.MaxQueued, hour.Fetches, cfg.MaxFetchesHour, day.Fetches, cfg.MaxFetchesDay, day.Created, cfg.MaxSubmitsPerDay, cfg.Paused())
		return nil
	}
	if fs.NArg() != 1 {
		return errors.New("status takes at most one job ID")
	}
	j, err := s.A.GetJob(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	var c *archive.Capture
	if j.ResultCaptureID != "" {
		cc, err := s.A.Status(ctx, j.ResultCaptureID)
		if err != nil {
			return err
		}
		c = &cc
	}
	if *asJSON {
		return printJSON(out, map[string]any{"job": j, "capture": c})
	}
	fmt.Fprintf(out, "job %s: %s (attempts %d of %d)\n  url: %s\n", j.JobID, j.State, j.AttemptsMade, j.MaxAttempts, j.URL)
	if c != nil {
		fmt.Fprintf(out, "  capture: %s\n  object: %s (%d bytes, %s), completeness %s\n", c.CaptureID, c.ObjectID, c.Size, c.MediaType, c.Completeness)
	}
	for _, at := range j.Attempts {
		fmt.Fprintf(out, "  attempt %d by %s: %s at %s: %s\n", at.Number, at.Owner, at.Outcome, at.FinishedAt, at.Detail)
	}
	return nil
}

func worker(cfg trial.Config, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	once := fs.Bool("once", false, "run due jobs until none can run now, then exit")
	poll := fs.Duration("poll", 15*time.Second, "wait between checks when nothing can run")
	lease := fs.Duration("lease", 5*time.Minute, "how long one claim holds a job")
	if err := fs.Parse(args); err != nil {
		return orHelp(err)
	}
	if *poll <= 0 || *lease <= 0 {
		return errors.New("--poll and --lease must be positive")
	}
	s, err := open(cfg)
	if err != nil {
		return err
	}
	defer s.Close()
	s.Lease = *lease
	fmt.Fprintln(out, s.Summary())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return s.Loop(ctx, trial.LoopOptions{Poll: *poll, Once: *once}, out, errOut)
}

func orHelp(err error) error {
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

func printJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
