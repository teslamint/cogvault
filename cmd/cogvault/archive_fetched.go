package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/teslamint/cogvault/internal/archive"
	"github.com/teslamint/cogvault/internal/terms"
)

func runArchiveReassess(cmd *cobra.Command, args []string) error {
	a, err := openArchive(cmd)
	if err != nil {
		return err
	}
	defer a.Close()
	basis, _ := cmd.Flags().GetString("basis")
	res, err := a.Reassess(cmd.Context(), args[0], basis)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "reassessed %s as %s (verified on your stated basis; run handoff to pass it to ingest)\n", args[0], res.CaptureID)
	return nil
}

// maxFetchFileBytes bounds a fetch file: the 10 MiB body limit plus room
// for the frontmatter fetch adds.
const maxFetchFileBytes = maxFetchBytes + 1<<20

// fetchMetadataKeys are the keys cogvault fetch writes.
var fetchMetadataKeys = map[string]bool{"source_url": true, "fetched_at": true, "usage_terms": true}

// splitFetchFile separates a cogvault fetch file into its metadata and the
// response body. It accepts the split only if encoding the metadata and body
// again with terms.Encode, as fetch does, gives back the file byte for byte,
// so the body is exactly what fetch received; anything else is refused
// rather than guessed.
func splitFetchFile(data []byte) (sourceURL, fetchedAt string, body []byte, err error) {
	if !bytes.HasPrefix(data, []byte("---\n")) {
		return "", "", nil, fmt.Errorf("not a cogvault fetch file: no frontmatter")
	}
	meta, body, err := terms.Parse(data)
	if err != nil {
		return "", "", nil, fmt.Errorf("not a cogvault fetch file: %w", err)
	}
	var unknown []string
	for k := range meta {
		if !fetchMetadataKeys[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return "", "", nil, fmt.Errorf("not a cogvault fetch file: unexpected frontmatter keys %s", strings.Join(unknown, ", "))
	}
	sourceURL, ok1 := meta["source_url"].(string)
	fetchedAt, ok2 := meta["fetched_at"].(string)
	if !ok1 || !ok2 || sourceURL == "" || fetchedAt == "" {
		return "", "", nil, fmt.Errorf("not a cogvault fetch file: source_url and fetched_at must be strings")
	}
	again, err := terms.Encode(meta, body)
	if err != nil || !bytes.Equal(again, data) {
		return "", "", nil, fmt.Errorf("not a cogvault fetch file: frontmatter and body do not re-encode to the same bytes")
	}
	return sourceURL, fetchedAt, body, nil
}

func runArchiveImportFetched(cmd *cobra.Command, _ []string) error {
	path, _ := cmd.Flags().GetString("file")
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("--file %s: not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFetchFileBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxFetchFileBytes {
		return fmt.Errorf("--file %s: larger than a fetch file can be", path)
	}
	sourceURL, fetchedAt, body, err := splitFetchFile(data)
	if err != nil {
		return fmt.Errorf("--file %s: %w", path, err)
	}
	a, err := openArchive(cmd)
	if err != nil {
		return err
	}
	defer a.Close()
	key, _ := cmd.Flags().GetString("key")
	res, err := a.ImportFetched(cmd.Context(), archive.FetchedRequest{
		Key: key, Body: body, URL: sourceURL, FetchedAt: fetchedAt,
		DerivedName: filepath.Base(path), Derived: data,
	})
	if err != nil {
		return err
	}
	printResult(cmd.OutOrStdout(), res)
	fmt.Fprintf(cmd.OutOrStdout(), "body %d bytes stored as the original; %s recorded as derived from it (completeness unknown)\n", len(body), filepath.Base(path))
	return nil
}

func runArchiveAcquire(cmd *cobra.Command, args []string) error {
	recoverID, _ := cmd.Flags().GetString("recover")
	if (recoverID == "") == (len(args) == 0) {
		return fmt.Errorf("give either a URL or --recover <capture-id>")
	}
	a, err := openArchive(cmd)
	if err != nil {
		return err
	}
	defer a.Close()
	var q archive.Acquirer
	var res archive.Result
	var rec archive.AcquisitionRecord
	if recoverID != "" {
		cause, _ := cmd.Flags().GetString("cause")
		res, rec, err = a.Reacquire(cmd.Context(), q, recoverID, cause)
	} else {
		res, rec, err = a.Acquire(cmd.Context(), q, args[0])
	}
	if err != nil {
		return err
	}
	printResult(cmd.OutOrStdout(), res)
	fmt.Fprintf(cmd.OutOrStdout(), "downloaded %d bytes from %s in %d request(s); completeness unknown\n", rec.Size, rec.FinalURL, len(rec.Hops))
	return nil
}

func runArchiveExtract(cmd *cobra.Command, args []string) error {
	a, err := openArchive(cmd)
	if err != nil {
		return err
	}
	defer a.Close()
	e, err := a.ExtractText(cmd.Context(), args[0])
	if err != nil {
		if e.Status == "failed" {
			return fmt.Errorf("extraction failed and was recorded: %w", err)
		}
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s %s: %s (%d bytes)\n", e.Extractor, e.Status, e.OutputObjectID, e.Size)
	return nil
}
