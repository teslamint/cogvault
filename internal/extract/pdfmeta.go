package extract

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/url"
	"os/exec"
	"strings"
	"unicode"
)

// PDFMetadata contains only values read from PDF metadata and link annotations.
// Links are destinations, not a claim that any one is the canonical source URL.
// Title and Author are declarations inside untrusted PDF bytes, not verified identity.
type PDFMetadata struct {
	Title  string
	Author string
	Links  []string
}

func ReadPDFMetadata(ctx context.Context, path string, commands Commands) (PDFMetadata, error) {
	command := commands.CommandContext
	if command == nil {
		command = exec.CommandContext
	}
	const capBytes = 128 << 10
	// read returns pdfinfo stdout. Over capBytes it fails, unless cut is set:
	// then it stops pdfinfo and keeps the complete lines read so far.
	read := func(cut bool, args ...string) ([]byte, error) {
		cmd := command(ctx, "pdfinfo", args...)
		out, err := cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(io.LimitReader(out, capBytes+1))
		over := len(data) > capBytes
		if over || readErr != nil {
			_ = cmd.Process.Kill()
		}
		waitErr := cmd.Wait()
		if readErr != nil {
			return nil, readErr
		}
		if over {
			if !cut {
				return nil, fmt.Errorf("pdfinfo output exceeds %d bytes", capBytes)
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			// The kill makes Wait fail; the lines before the cut are complete.
			return data[:bytes.LastIndexByte(data[:capBytes], '\n')+1], nil
		}
		return data, waitErr
	}
	info, err := read(false, path)
	if err != nil {
		return PDFMetadata{}, fmt.Errorf("pdfinfo metadata: %w", err)
	}
	result := parsePDFInfo(string(info))
	// Only the first 64 links are kept, so a link list over the cap is cut
	// rather than failing the read and losing the declared title and author.
	links, err := read(true, "-url", path)
	if err != nil {
		return PDFMetadata{}, fmt.Errorf("pdfinfo links: %w", err)
	}
	result.Links = parsePDFLinks(string(links))
	return result, nil
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
