package extract

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"unicode"

	"golang.org/x/sys/unix"
)

// PDFMetadata contains only values read from PDF metadata and link annotations.
// Links are destinations, not a claim that any one is the canonical source URL.
// Title and Author are declarations inside untrusted PDF bytes, not verified identity.
type PDFMetadata struct {
	Title  string
	Author string
	Links  []string
}

// ErrPDFLinksUnavailable reports that the declared fields were read but the
// link listing was not, for example with a pdfinfo that lacks -url.
var ErrPDFLinksUnavailable = errors.New("pdf links unavailable")

// pdfInfoCap bounds each pdfinfo output read.
const pdfInfoCap = 128 << 10

// pdfInfoSource is the path pdfinfo reads: the source descriptor passed as
// its first extra file, so pdfinfo never resolves the source path itself.
const pdfInfoSource = "/dev/fd/3"

// ReadPDFMetadata reads the declared title and author and the link
// annotations. When only the link listing fails, it returns the declared
// fields with an error wrapping ErrPDFLinksUnavailable. The source is opened
// without following a final symlink and must be a regular file.
func ReadPDFMetadata(ctx context.Context, path string, commands Commands) (PDFMetadata, error) {
	command := commands.CommandContext
	if command == nil {
		command = exec.CommandContext
	}
	src, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return PDFMetadata{}, fmt.Errorf("pdf metadata source: %w", err)
	}
	defer src.Close()
	if info, err := src.Stat(); err != nil {
		return PDFMetadata{}, fmt.Errorf("pdf metadata source: %w", err)
	} else if !info.Mode().IsRegular() {
		return PDFMetadata{}, errors.New("pdf metadata source is not a regular file")
	}
	info, err := runPDFInfo(ctx, command, src, false, pdfInfoSource)
	if err != nil {
		return PDFMetadata{}, fmt.Errorf("pdfinfo metadata: %w", err)
	}
	result := parsePDFInfo(string(info))
	// Only the first 64 links are kept, so a link list over the cap is cut
	// rather than failing the read and losing the declared title and author.
	links, err := runPDFInfo(ctx, command, src, true, "-url", pdfInfoSource)
	if err != nil {
		return result, fmt.Errorf("%w: pdfinfo links: %v", ErrPDFLinksUnavailable, err)
	}
	result.Links = parsePDFLinks(string(links))
	return result, nil
}

// runPDFInfo returns pdfinfo stdout. Over pdfInfoCap it fails, unless cut is
// set: then it stops pdfinfo and keeps the complete lines read so far.
func runPDFInfo(ctx context.Context, command func(context.Context, string, ...string) *exec.Cmd, src *os.File, cut bool, args ...string) ([]byte, error) {
	cmd := command(ctx, "pdfinfo", args...)
	cmd.ExtraFiles = []*os.File{src}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(out, pdfInfoCap+1))
	over := len(data) > pdfInfoCap
	if over || readErr != nil {
		_ = cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	switch {
	case readErr != nil:
		return nil, readErr
	case !over:
		return data, waitErr
	case !cut:
		return nil, fmt.Errorf("pdfinfo output exceeds %d bytes", pdfInfoCap)
	case ctx.Err() != nil:
		return nil, ctx.Err()
	}
	// The kill makes Wait fail; the lines before the cut are complete.
	return data[:bytes.LastIndexByte(data[:pdfInfoCap], '\n')+1], nil
}

func parsePDFInfo(info string) PDFMetadata {
	var result PDFMetadata
	for _, line := range strings.Split(string(info), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) > 1024 || strings.IndexFunc(value, unicode.IsControl) >= 0 {
			continue
		}
		switch key {
		case "Title":
			result.Title = value
		case "Author":
			result.Author = value
		}
	}
	return result
}

func parsePDFLinks(links string) []string {
	var result []string
	seen := map[string]bool{}
	for _, line := range strings.Split(string(links), "\n") {
		parts := strings.Fields(line)
		if len(parts) < 3 || parts[1] != "Annotation" {
			continue
		}
		destination := strings.Join(parts[2:], " ")
		u, err := url.Parse(destination)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || len(destination) > 2048 || strings.IndexFunc(destination, unicode.IsSpace) >= 0 || strings.IndexFunc(destination, unicode.IsControl) >= 0 || seen[destination] {
			continue
		}
		seen[destination] = true
		result = append(result, destination)
		if len(result) == 64 {
			break
		}
	}
	return result
}
