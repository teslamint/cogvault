package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"syscall"

	"github.com/teslamint/cogvault/internal/archive"
	"github.com/teslamint/cogvault/internal/trial"
)

// selectedFile is available only to the local operator CLI. No MCP argument
// reaches it. Refuse non-regular input, a final symlink, replacement between
// checks and a file larger than the command's bound before reading its bytes.
func selectedFile(path string, limit int64) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() > limit {
		return nil, errors.New("selected input must be a bounded regular file, not a symlink")
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errors.New("selected input could not be opened")
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Size() > limit {
		return nil, errors.New("selected input changed or exceeds its size bound")
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, errors.New("selected input could not be read within its size bound")
	}
	return b, nil
}

func importManifest(cfg trial.Config, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("import-manifest", flag.ContinueOnError)
	file := fs.String("file", "", "selected source manifest JSON only (not a raw report)")
	if err := fs.Parse(args); err != nil {
		return orHelp(err)
	}
	if *file == "" || fs.NArg() != 0 {
		return errors.New("import-manifest requires --file and no positional arguments")
	}
	if err := cfg.CheckRoot(); err != nil {
		return err
	}
	b, err := selectedFile(*file, 8<<10)
	if err != nil {
		return err
	}
	s, err := open(cfg)
	if err != nil {
		return err
	}
	defer s.Close()
	res, err := s.ImportManifest(context.Background(), string(b))
	if err != nil {
		return err
	}
	return printJSON(out, res)
}

func reportStatus(cfg trial.Config, args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("report-status takes exactly one report ID")
	}
	s, err := open(cfg)
	if err != nil {
		return err
	}
	defer s.Close()
	res, err := s.ReportStatus(context.Background(), args[0])
	if err != nil {
		return err
	}
	return printJSON(out, res)
}

func importOriginal(cfg trial.Config, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("import-original", flag.ContinueOnError)
	report := fs.String("report", "", "imported report ID")
	source := fs.String("source", "", "source ID in that manifest")
	file := fs.String("file", "", "explicitly selected local file; no directory or symlink")
	hash := fs.String("sha256", "", "expected sha256:<64 lowercase hex>")
	size := fs.Int64("bytes", 0, "expected exact file size")
	media := fs.String("media", "", "application/pdf or text/html")
	completeness := fs.String("completeness", "", "unknown or incomplete; never auto-verified")
	method := fs.String("method", "", "existing_file, browser_download or direct_https (operator statement)")
	if err := fs.Parse(args); err != nil {
		return orHelp(err)
	}
	if fs.NArg() != 0 || *report == "" || *source == "" || *file == "" || *hash == "" || *size <= 0 || *size > archive.MaxObjectBytes || *media == "" || *completeness == "" || *method == "" {
		return errors.New("import-original requires report, source, file, sha256, bounded bytes, media, completeness and method")
	}
	if err := cfg.CheckRoot(); err != nil {
		return err
	}
	b, err := selectedFile(*file, archive.MaxObjectBytes)
	if err != nil {
		return err
	}
	s, err := open(cfg)
	if err != nil {
		return err
	}
	defer s.Close()
	res, err := s.ImportOriginal(context.Background(), *report, *source, trial.OriginalImport{Content: bytes.NewReader(b), ExpectedSHA256: *hash, ExpectedSize: *size, MediaType: *media, Completeness: *completeness, AcquisitionMethod: *method})
	if err != nil {
		return err
	}
	return printJSON(out, res)
}
