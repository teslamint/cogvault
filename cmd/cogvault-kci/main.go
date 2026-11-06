// Command cogvault-kci is the browser worker of the collection queue (SPEC
// §9.16 "Collection jobs", ADR 0027 "KCI official-browser executor"). It runs
// `executor=browser` jobs by opening the KCI article detail page in a headless
// browser and clicking the portal's own "KCI 원문 내려받기" control.
//
// It is a separate binary on purpose: the browser dependency and its attack
// surface stay out of `cogvault`, the MCP server image. It has the same
// unconditional activation gate as `cogvault archive job run` (it needs an
// explicit --config, and claims nothing unless archive.root matches,
// archive.capture_enabled is true, and the ADR 0027 item 14 check passes), and
// no flag skips it. It needs a Chrome or Chromium on the host.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/teslamint/cogvault/internal/archive"
	"github.com/teslamint/cogvault/internal/archivegate"
	"github.com/teslamint/cogvault/internal/kcibrowser"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "cogvault-kci:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("cogvault-kci", flag.ContinueOnError)
	root := fs.String("root", "", "archive root directory (required)")
	config := fs.String("config", "", "config file naming archive.root and the activation attestations (required)")
	owner := fs.String("owner", "", "worker name recorded on each attempt (default: host and PID)")
	lease := fs.Duration("lease", 10*time.Minute, "how long one claim holds a job")
	poll := fs.Duration("poll", 30*time.Second, "wait between checks when no job is due")
	once := fs.Bool("once", false, "run due jobs until none is due, then exit")
	chrome := fs.String("chrome", "", "path to the Chrome or Chromium binary (default: find one)")
	noSandbox := fs.Bool("no-sandbox", false, "disable Chrome's sandbox; only for a non-root container that cannot use it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *root == "" || *config == "" {
		return fmt.Errorf("--root and --config are required: the worker runs only behind the ADR 0027 activation gate")
	}
	if *poll <= 0 || *lease <= 0 {
		return fmt.Errorf("--poll and --lease must be positive")
	}
	absRoot, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	if *owner == "" {
		host, _ := os.Hostname()
		*owner = fmt.Sprintf("kci-%s-%d", host, os.Getpid())
	}
	driver := kcibrowser.New(kcibrowser.Options{ChromePath: *chrome, NoSandbox: *noSandbox})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return archivegate.RunLoop(ctx,
		func() (*archive.Archive, error) { return archive.Open(absRoot) },
		func() error { return archivegate.WorkerAllowed(*config, absRoot, archivegate.ProductionActivation) },
		func(ctx context.Context, a *archive.Archive) (*archive.Job, error) {
			return a.RunNextBrowserJob(ctx, driver, *owner, *lease)
		},
		archivegate.Options{Poll: *poll, Once: *once}, os.Stdout, os.Stderr)
}
