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

// logOnce prints a line only when it differs from the previous one of its kind,
// so a fault that persists is logged once, not every poll.
type logOnce struct {
	w    io.Writer
	last string
}

func (l *logOnce) say(format, msg string) {
	if msg != l.last {
		fmt.Fprintf(l.w, format, msg)
		l.last = msg
	}
}

func (l *logOnce) reset() { l.last = "" }

// next says what the loop does after one pass.
type next int

const (
	nextNow  next = iota // a job just finished: run again at once
	nextWait             // nothing to do now: sleep one poll
	nextStop             // a --once run is done
)

type logs struct{ out, err io.Writer }

// Loop is the trial worker. It pauses (and says so once per change) when the
// root check fails or the PAUSE file exists, and it never exits for that reason:
// a restart policy would only restart it. A stop signal ends ctx; a download in
// progress is abandoned without a record and its lease expiry lets the next
// start resume the job. Nothing here deletes anything.
func (s *Service) Loop(ctx context.Context, o LoopOptions, out, errOut io.Writer) error {
	l := logs{out: out, err: errOut}
	pause, wait, fault := &logOnce{w: errOut}, &logOnce{w: errOut}, &logOnce{w: errOut}
	for {
		step, err := s.pass(ctx, o, l, pause, wait, fault)
		switch {
		case err != nil:
			return err
		case ctx.Err() != nil || step == nextStop:
			return nil
		case step == nextNow:
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(o.Poll):
		}
	}
}

// pass is one turn of the loop. A non-nil error ends the loop.
func (s *Service) pass(ctx context.Context, o LoopOptions, l logs, pause, wait, fault *logOnce) (next, error) {
	if gate := s.Allowed(); gate != nil {
		if o.Once {
			return nextStop, gate
		}
		pause.say("paused: %s\n", gate.Error())
		return nextWait, nil
	}
	if pause.last != "" {
		fmt.Fprintln(l.err, "resumed")
		pause.reset()
	}
	j, why, err := s.RunOnce(ctx)
	switch {
	case ctx.Err() != nil:
		return nextStop, nil
	case errors.Is(err, archive.ErrLeaseLost):
		// Another worker reclaimed the job; its try is the one that counts.
		fmt.Fprintf(l.err, "job %s: %v\n", j.JobID, err)
	case err != nil:
		if o.Once {
			fmt.Fprintf(l.err, "worker error: %s\n", err.Error())
			return nextStop, err
		}
		fault.say("worker error: %s\n", err.Error())
	case j != nil:
		fault.reset()
		wait.reset()
		fmt.Fprintf(l.out, "job %s: %s (attempt %d of %d)\n", j.JobID, j.State, j.AttemptsMade, j.MaxAttempts)
		return nextNow, nil
	case why != "":
		wait.say("waiting: %s\n", why)
	default:
		wait.reset()
	}
	if o.Once {
		return nextStop, nil
	}
	return nextWait, nil
}
