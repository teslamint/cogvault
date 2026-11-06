package archivegate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/teslamint/cogvault/internal/archive"
)

// Options are a worker loop's settings.
type Options struct {
	Poll time.Duration
	Once bool
}

// RunOne runs at most one due job on an open archive and returns it, or nil
// when none was due. It is the only part that differs between workers (the
// HTTP worker and the browser worker).
type RunOne func(ctx context.Context, a *archive.Archive) (*archive.Job, error)

// RunLoop is a worker loop, separated from every command so a package test
// can drive it on a temporary root with an injected gate. A command always
// passes the real activation gate; there is no flag or setting that skips it.
//
// allowed is checked before the archive is opened and before every claim, and
// the loop claims nothing while it returns an error. With Once, a closed gate
// is an error returned before anything is opened. A long-running worker
// instead pauses and logs once per change: with a restart policy an exit
// would be a crash loop, and flipping archive.capture_enabled on is how an
// operator resumes it. The archive is opened only after the gate first
// passes, so a gated worker creates nothing under its root. ctx ends the
// loop: a download in progress is abandoned without a record and its lease
// expiry lets the next worker resume the job.
func RunLoop(ctx context.Context, open func() (*archive.Archive, error), allowed func() error, run RunOne, o Options, out, errOut io.Writer) error {
	var a *archive.Archive
	defer func() {
		if a != nil {
			a.Close()
		}
	}()
	lastBlocked := ""
	for {
		if err := allowed(); err != nil {
			if o.Once {
				return err
			}
			if msg := err.Error(); msg != lastBlocked {
				fmt.Fprintf(errOut, "paused: %s\n", msg)
				lastBlocked = msg
			}
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(o.Poll):
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
		j, err := run(ctx, a)
		switch {
		case ctx.Err() != nil:
			return nil
		case errors.Is(err, archive.ErrLeaseLost):
			// Another worker reclaimed the job; its try is the one that counts.
			fmt.Fprintf(errOut, "job %s: %v\n", j.JobID, err)
		case err != nil:
			fmt.Fprintf(errOut, "worker error: %v\n", err)
			if o.Once {
				return err
			}
		case j != nil:
			fmt.Fprintf(out, "job %s: %s (attempt %d of %d)\n", j.JobID, j.State, j.AttemptsMade, j.MaxAttempts)
			continue
		case o.Once:
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(o.Poll):
		}
	}
}
