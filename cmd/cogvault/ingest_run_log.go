package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/teslamint/cogvault/internal/ingest"
)

// runLogNameLayout is the timestamp in a run log file name. It is ISO 8601
// basic format because Finder shows ':' in file names as '/'.
const runLogNameLayout = "20060102T150405-0700"

var ingestPID = os.Getpid

// runLog is one ingest run's own log file,
// ingest.<start>.<pid>.err.log in the --run-log-dir directory. The file is
// created on the first state-change event, or at the end of a run that did
// not finish ok, so hourly no-op runs leave no file. Each line is written
// unbuffered, so a killed run keeps every line written before the kill.
// Failures to create or write the file are logged and never fail the run.
// A nil *runLog records nothing.
type runLog struct {
	path      string
	startLine string
	f         *os.File
	broken    bool
}

func newRunLog(dir string, started time.Time, startLine string) *runLog {
	if dir == "" {
		return nil
	}
	name := fmt.Sprintf("ingest.%s.%d.err.log", started.Format(runLogNameLayout), ingestPID())
	return &runLog{path: filepath.Join(dir, name), startLine: startLine}
}

func (l *runLog) open() bool {
	if l.f != nil {
		return true
	}
	if l.broken {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
		return l.fail("create run log dir", err)
	}
	f, err := os.OpenFile(l.path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return l.fail("create run log", err)
	}
	l.f = f
	return l.write(l.startLine)
}

func (l *runLog) write(line string) bool {
	if _, err := io.WriteString(l.f, line+"\n"); err != nil {
		l.f.Close()
		l.f = nil
		return l.fail("write run log", err)
	}
	return true
}

func (l *runLog) fail(op string, err error) bool {
	l.broken = true
	slog.Warn("ingest: "+op+" failed", "path", l.path, "error", err)
	return false
}

func (l *runLog) event(at time.Time, f ingest.FileResult) {
	if !l.open() {
		return
	}
	line := fmt.Sprintf("%s %s %s", at.Format(ingestRunTimeLayout), f.Action, f.Path)
	if f.Error != "" {
		line += "  " + f.Error
	}
	l.write(line)
}

// finish writes the end line and, when present, the run error or panic line.
func (l *runLog) finish(endLine string, ok bool, detail string) {
	if l == nil || (l.f == nil && ok) || !l.open() {
		return
	}
	if l.write(endLine) && detail != "" {
		l.write(detail)
	}
	if l.f != nil {
		l.f.Close()
		l.f = nil
	}
}
