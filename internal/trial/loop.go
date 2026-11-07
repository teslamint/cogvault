package trial

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/teslamint/cogvault/internal/archive"
)

// LoopOptions are the worker loop's settings.
type LoopOptions struct {
	Poll time.Duration
	// Once runs due jobs until none can run now, then returns.
	Once bool
}

// Loop is the trial worker. It pauses (and says so once per change) when the
// root check fails or the PAUSE file exists, and it never exits for that reason:
// a restart policy would only restart it. A stop signal ends ctx; a download in
// progress is abandoned without a record and its lease expiry lets the next
// start resume the job. Nothing here deletes anything.
func (s *Service) Loop(ctx context.Context, o LoopOptions, out, errOut io.Writer) error {
	lastPause, lastWait, lastErr := "", "", ""
	sleep := func() bool {
		select {
		case <-ctx.Done():
			return false
		case <-time.After(o.Poll):
			return true
		}
	}
	for {
		if err := s.Allowed(); err != nil {
			if o.Once {
				return err
			}
			if msg := err.Error(); msg != lastPause {
				fmt.Fprintf(errOut, "paused: %s\n", msg)
				lastPause = msg
			}
			if !sleep() {
				return nil
			}
			continue
		}
		if lastPause != "" {
			fmt.Fprintln(errOut, "resumed")
			lastPause = ""
		}
		j, why, err := s.RunOnce(ctx)
		switch {
		case ctx.Err() != nil:
			return nil
		case errors.Is(err, archive.ErrLeaseLost):
			fmt.Fprintf(errOut, "job %s: %v\n", j.JobID, err)
		case err != nil:
			if msg := err.Error(); msg != lastErr || o.Once {
				fmt.Fprintf(errOut, "worker error: %s\n", msg)
				lastErr = msg
			}
			if o.Once {
				return err
			}
		case j != nil:
			lastErr, lastWait = "", ""
			fmt.Fprintf(out, "job %s: %s (attempt %d of %d)\n", j.JobID, j.State, j.AttemptsMade, j.MaxAttempts)
			continue
		case why != "":
			if why != lastWait {
				fmt.Fprintf(errOut, "waiting: %s\n", why)
				lastWait = why
			}
			if o.Once {
				return nil
			}
		default:
			lastWait = ""
			if o.Once {
				return nil
			}
		}
		if !sleep() {
			return nil
		}
	}
}
