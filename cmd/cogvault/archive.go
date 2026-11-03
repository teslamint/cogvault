package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/teslamint/cogvault/internal/archive"
	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/ingest"
)

// newArchiveCmd is the local prototype of the research source archive
// (ADR 0027, proposed). Archive-host commands read no config file: every
// call names its root, so they cannot touch the configured wiki, sources,
// or ingest ledger. The ingest-host commands (pickup, receipts) read the
// config only to find the source directory, its types, and the ledger, and
// never open the archive registry.
func newArchiveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "archive",
		Short: "Experimental local prototype: record source originals and research reports",
	}
	cmd.PersistentFlags().String("root", "", "archive root directory (required)")
	_ = cmd.MarkPersistentFlagRequired("root")

	capture := &cobra.Command{
		Use:   "capture",
		Short: "Store a source original and record where it came from",
		Args:  cobra.NoArgs,
		RunE:  runArchiveCapture,
	}
	capture.Flags().String("file", "", "source file to store (required)")
	capture.Flags().String("url", "", "http(s) URL the source came from")
	capture.Flags().String("supplier", "", "who supplied the file")
	capture.Flags().String("title", "", "source title")
	capture.Flags().String("media-type", "", "media type (default: sniffed from content)")
	capture.Flags().String("completeness", "unknown", "verified, unknown, or incomplete")
	capture.Flags().String("basis", "", "checkable evidence for the completeness judgment (required for verified)")
	capture.Flags().String("key", "", "idempotency key (default: the request digest)")
	_ = capture.MarkFlagRequired("file")

	report := &cobra.Command{
		Use:   "import-report",
		Short: "Store a research report and the sources it cites",
		Args:  cobra.NoArgs,
		RunE:  runArchiveImportReport,
	}
	report.Flags().String("file", "", "report file to store (required)")
	report.Flags().String("supplier", "", "who produced the report")
	report.Flags().String("title", "", "report title")
	report.Flags().StringArray("cite", nil, "cited source capture ID or http(s) URL, in report order (repeatable)")
	report.Flags().String("key", "", "idempotency key (default: the request digest)")
	_ = report.MarkFlagRequired("file")

	status := &cobra.Command{
		Use:   "status [capture-id]",
		Short: "Show one capture with its relations, or list all captures",
		Args:  cobra.MaximumNArgs(1),
		RunE:  runArchiveStatus,
	}
	status.Flags().Bool("json", false, "output as JSON")

	summary := &cobra.Command{
		Use:   "summary",
		Short: "Store a finished summary of a source (never recorded as the original)",
		Args:  cobra.NoArgs,
		RunE:  runArchiveSummary,
	}
	summary.Flags().String("file", "", "summary file to store (required)")
	summary.Flags().String("of", "", "summarized source capture ID")
	summary.Flags().String("url", "", "summarized http(s) URL")
	summary.Flags().String("supplier", "", "who wrote the summary")
	summary.Flags().String("title", "", "summary title")
	summary.Flags().String("key", "", "idempotency key (default: the request digest)")
	_ = summary.MarkFlagRequired("file")

	attempt := &cobra.Command{
		Use:   "attempt <capture-id>",
		Short: "Append one recovery attempt for an incomplete or missing original",
		Args:  cobra.ExactArgs(1),
		RunE:  runArchiveAttempt,
	}
	attempt.Flags().String("cause", "", "citation_only, truncated, access_restricted, missing_parts, edition_mismatch, extraction_failed, or other (required)")
	attempt.Flags().String("tried", "", "what was tried (required)")
	attempt.Flags().String("result", "", "acquired, partial, failed, or human_action_required (required)")
	attempt.Flags().String("result-capture", "", "new source capture holding what was recovered (required for acquired or partial)")
	attempt.Flags().String("gap", "", "what is still missing")
	attempt.Flags().String("next", "", "next action")
	_ = attempt.MarkFlagRequired("cause")
	_ = attempt.MarkFlagRequired("tried")
	_ = attempt.MarkFlagRequired("result")

	handoff := &cobra.Command{
		Use:   "handoff",
		Short: "Write hand-off manifests for verified source originals; report held ones",
		Args:  cobra.NoArgs,
		RunE:  runArchiveHandoff,
	}
	handoff.Flags().StringSlice("types", archive.DefaultHandoffTypes, "extensions the ingest host digests (txt only for the openai backend)")
	handoff.Flags().Bool("json", false, "output as JSON")

	collect := &cobra.Command{
		Use:   "collect-receipts",
		Short: "Append new ingest-host receipts to the registry unchanged",
		Args:  cobra.NoArgs,
		RunE:  runArchiveCollectReceipts,
	}

	pickup := &cobra.Command{
		Use:   "pickup",
		Short: "Ingest host: copy handed-off objects into a configured source directory",
		Args:  cobra.NoArgs,
		RunE:  runArchivePickup,
	}
	pickup.Flags().String("dest", "", "configured source directory to copy into (required)")
	pickup.Flags().Bool("json", false, "output as JSON")
	_ = pickup.MarkFlagRequired("dest")

	receipts := &cobra.Command{
		Use:   "receipts",
		Short: "Ingest host: write receipts that copy what the ingest ledger says",
		Args:  cobra.NoArgs,
		RunE:  runArchiveReceipts,
	}
	receipts.Flags().String("dest", "", "configured source directory the objects were copied into (required)")
	_ = receipts.MarkFlagRequired("dest")

	pageLinks := &cobra.Command{
		Use:   "page-links <wiki-page>",
		Short: "List the captures linked to a wiki source page (the full list; page frontmatter is a digest-time snapshot)",
		Args:  cobra.ExactArgs(1),
		RunE:  runArchivePageLinks,
	}
	pageLinks.Flags().Bool("json", false, "output as JSON")

	cmd.AddCommand(capture, report, status, summary, attempt, handoff, collect, pickup, receipts, pageLinks)
	cmd.AddCommand(newArchiveOperatorCmds()...)
	return cmd
}

func openArchive(cmd *cobra.Command) (*archive.Archive, error) {
	root, _ := cmd.Flags().GetString("root")
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return archive.Open(abs)
}

// openSourceFile opens a regular file; the name kept is the base name only,
// so no host path enters the registry.
func openSourceFile(cmd *cobra.Command) (*os.File, string, error) {
	path, _ := cmd.Flags().GetString("file")
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, "", err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, "", fmt.Errorf("--file %s: not a regular file", path)
	}
	return f, filepath.Base(path), nil
}

func printResult(w io.Writer, res archive.Result) {
	state := "created"
	if res.Existing {
		state = "existing"
	}
	fmt.Fprintf(w, "%s %s %s\n", state, res.CaptureID, res.ObjectID)
}

func runArchiveCapture(cmd *cobra.Command, _ []string) error {
	a, err := openArchive(cmd)
	if err != nil {
		return err
	}
	defer a.Close()
	f, name, err := openSourceFile(cmd)
	if err != nil {
		return err
	}
	defer f.Close()
	flag := func(n string) string { v, _ := cmd.Flags().GetString(n); return v }
	res, err := a.CaptureSource(cmd.Context(), archive.SourceRequest{
		Key: flag("key"), Content: f, OriginalName: name, URL: flag("url"),
		Supplier: flag("supplier"), Title: flag("title"), MediaType: flag("media-type"),
		Completeness: flag("completeness"), Basis: flag("basis"),
	})
	if err != nil {
		return err
	}
	printResult(cmd.OutOrStdout(), res)
	return nil
}

func runArchiveImportReport(cmd *cobra.Command, _ []string) error {
	a, err := openArchive(cmd)
	if err != nil {
		return err
	}
	defer a.Close()
	f, name, err := openSourceFile(cmd)
	if err != nil {
		return err
	}
	defer f.Close()
	flag := func(n string) string { v, _ := cmd.Flags().GetString(n); return v }
	cites, _ := cmd.Flags().GetStringArray("cite")
	res, err := a.ImportReport(cmd.Context(), archive.ReportRequest{
		Key: flag("key"), Content: f, OriginalName: name,
		Supplier: flag("supplier"), Title: flag("title"), Cites: cites,
	})
	if err != nil {
		return err
	}
	printResult(cmd.OutOrStdout(), res)
	return nil
}

func runArchiveStatus(cmd *cobra.Command, args []string) error {
	a, err := openArchive(cmd)
	if err != nil {
		return err
	}
	defer a.Close()
	asJSON, _ := cmd.Flags().GetBool("json")
	w := cmd.OutOrStdout()
	if len(args) == 0 {
		list, err := a.List(cmd.Context())
		if err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(w).Encode(list)
		}
		for _, c := range list {
			label := c.Title
			if label == "" {
				label = c.URL
			}
			if label == "" {
				label = c.OriginalName
			}
			fmt.Fprintf(w, "%s  %-6s  %-10s  %s\n", c.CaptureID, c.Kind, c.Completeness, label)
		}
		return nil
	}
	c, err := a.Status(cmd.Context(), args[0])
	if err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(w).Encode(c)
	}
	fmt.Fprintf(w, "capture       %s\n", c.CaptureID)
	fmt.Fprintf(w, "kind          %s\n", c.Kind)
	fmt.Fprintf(w, "object        %s (%d bytes, %s)\n", c.ObjectID, c.Size, c.MediaType)
	for _, kv := range [][2]string{{"name", c.OriginalName}, {"url", c.URL}, {"supplier", c.Supplier}, {"title", c.Title}} {
		if kv[1] != "" {
			fmt.Fprintf(w, "%-13s %s\n", kv[0], kv[1])
		}
	}
	fmt.Fprintf(w, "acquisition   %s\n", c.Acquisition)
	fmt.Fprintf(w, "completeness  %s\n", c.Completeness)
	if c.CompletenessBasis != "" {
		fmt.Fprintf(w, "basis         %s\n", c.CompletenessBasis)
	}
	fmt.Fprintf(w, "recorded      %s\n", c.RecordedAt)
	if c.SummaryOf != "" {
		fmt.Fprintf(w, "summary of    %s\n", c.SummaryOf)
	}
	if len(c.Summaries) > 0 {
		fmt.Fprintf(w, "summaries     %s\n", strings.Join(c.Summaries, ", "))
	}
	if len(c.SameObject) > 0 {
		fmt.Fprintf(w, "same bytes    %s\n", strings.Join(c.SameObject, ", "))
	}
	if len(c.CitedBy) > 0 {
		fmt.Fprintf(w, "cited by      %s\n", strings.Join(c.CitedBy, ", "))
	}
	if len(c.Citations) > 0 {
		fmt.Fprintln(w, "citations")
		for _, cit := range c.Citations {
			if cit.CaptureID != "" {
				fmt.Fprintf(w, "  %d  %s  (captured source, completeness %s)\n", cit.Position, cit.CaptureID, cit.Completeness)
			} else {
				fmt.Fprintf(w, "  %d  %s  (not captured)\n", cit.Position, cit.URL)
			}
		}
	}
	for _, at := range c.Attempts {
		fmt.Fprintf(w, "attempt %d     %s: %s -> %s %s\n", at.AttemptID, at.Cause, at.Tried, at.Result, at.ResultCaptureID)
	}
	for _, at := range c.RecoveredBy {
		fmt.Fprintf(w, "recovered     from %s (attempt %d)\n", at.CaptureID, at.AttemptID)
	}
	fmt.Fprintf(w, "ingest        %s", c.Ingest.State)
	switch {
	case c.Ingest.State == "observed":
		fmt.Fprintf(w, " (ingest-host ledger said %q at %s; page %s)", c.Ingest.LedgerStatus, c.Ingest.ObservedAt, c.Ingest.WikiPage)
	case c.Ingest.Reason != "":
		fmt.Fprintf(w, " (%s)", c.Ingest.Reason)
	}
	fmt.Fprintln(w)
	if len(c.Ingest.RecoveredAs) > 0 {
		fmt.Fprintf(w, "recovered as  %s (this capture stays held; ingest goes through these)\n", strings.Join(c.Ingest.RecoveredAs, ", "))
	}
	if c.Ingest.EvidenceRef != "" {
		fmt.Fprintf(w, "ref candidate %s (ledger page path; not checked here, use it as a ref only once the page exists in the wiki)\n", c.Ingest.EvidenceRef)
	}
	for _, l := range c.PageLinks {
		fmt.Fprintf(w, "page link     %s (%s; basis receipt %s)\n", l.WikiPage, linkState(l), l.BasisReceipt)
	}
	for _, d := range c.Derived {
		fmt.Fprintf(w, "derived       %s %s (%s, %d bytes, stored as %s; not an original)\n", d.Kind, d.Name, d.SHA256[:12], d.Size, d.ObjectID)
	}
	if d := c.Download; d != nil {
		fmt.Fprintf(w, "downloaded    %s → %s (%d request(s), %s)\n", d.RequestedURL, d.FinalURL, len(d.Hops), d.FetchedAt)
	}
	for _, e := range c.Extractions {
		if e.Status == "succeeded" {
			fmt.Fprintf(w, "extracted     %s %s (%d bytes, stored as %s; derived text, not an original)\n", e.Extractor, e.Status, e.Size, e.OutputObjectID)
		} else {
			fmt.Fprintf(w, "extracted     %s %s: %s\n", e.Extractor, e.Status, e.Detail)
		}
	}
	fmt.Fprintln(w, "note          provenance records what was supplied; it is not a fact check")
	return nil
}

func linkState(l archive.PageLink) string {
	if l.Current {
		return "current; page existence not checked here"
	}
	return "not current: a later receipt observed another result"
}

func runArchivePageLinks(cmd *cobra.Command, args []string) error {
	a, err := openArchive(cmd)
	if err != nil {
		return err
	}
	defer a.Close()
	links, err := a.PageLinksFor(cmd.Context(), args[0])
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		return json.NewEncoder(w).Encode(links)
	}
	for _, l := range links {
		fmt.Fprintf(w, "%s  %s  %s (basis receipt %s)\n", l.CaptureID, l.ObjectID, linkState(l), l.BasisReceipt)
	}
	fmt.Fprintf(w, "%d capture(s) linked to %s\n", len(links), args[0])
	return nil
}

func runArchiveSummary(cmd *cobra.Command, _ []string) error {
	a, err := openArchive(cmd)
	if err != nil {
		return err
	}
	defer a.Close()
	f, name, err := openSourceFile(cmd)
	if err != nil {
		return err
	}
	defer f.Close()
	flag := func(n string) string { v, _ := cmd.Flags().GetString(n); return v }
	res, err := a.CaptureSummary(cmd.Context(), archive.SummaryRequest{
		Key: flag("key"), Content: f, OriginalName: name, SummaryOf: flag("of"),
		URL: flag("url"), Supplier: flag("supplier"), Title: flag("title"),
	})
	if err != nil {
		return err
	}
	printResult(cmd.OutOrStdout(), res)
	return nil
}

func runArchiveAttempt(cmd *cobra.Command, args []string) error {
	a, err := openArchive(cmd)
	if err != nil {
		return err
	}
	defer a.Close()
	flag := func(n string) string { v, _ := cmd.Flags().GetString(n); return v }
	at, err := a.RecordAttempt(cmd.Context(), archive.Attempt{
		CaptureID: args[0], Cause: flag("cause"), Tried: flag("tried"), Result: flag("result"),
		ResultCaptureID: flag("result-capture"), RemainingGap: flag("gap"), NextAction: flag("next"),
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "attempt %d recorded for %s\n", at.AttemptID, at.CaptureID)
	return nil
}

func runArchiveHandoff(cmd *cobra.Command, _ []string) error {
	a, err := openArchive(cmd)
	if err != nil {
		return err
	}
	defer a.Close()
	types, _ := cmd.Flags().GetStringSlice("types")
	res, err := a.Handoff(cmd.Context(), types)
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		return json.NewEncoder(w).Encode(res)
	}
	for _, r := range res {
		if r.Status == "held" {
			fmt.Fprintf(w, "held     %s  %s\n", r.CaptureID, r.Reason)
			continue
		}
		fmt.Fprintf(w, "%-8s %s  .%s\n", r.Status, r.CaptureID, r.Extension)
	}
	return nil
}

func runArchiveCollectReceipts(cmd *cobra.Command, _ []string) error {
	a, err := openArchive(cmd)
	if err != nil {
		return err
	}
	defer a.Close()
	n, err := a.CollectReceipts(cmd.Context())
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "collected %d receipt(s)\n", n)
	return nil
}

// pathInputTypes are the source types the path-input backends (claudecode,
// ollama) are verified to digest; .txt is verified only on openai.
var pathInputTypes = []string{"pdf", "md", "markdown"}

// ingestHostTypes returns the types the configured ingest digests from
// dest: the matching source's configured types that the backend supports.
func ingestHostTypes(cfg *config.Config, dest string) ([]string, error) {
	supported := pathInputTypes
	if cfg.LLM.Backend == "openai" {
		supported = archive.HandoffExtensions
	}
	for _, s := range cfg.Sources {
		if filepath.Clean(s.Path) != filepath.Clean(dest) {
			continue
		}
		var out []string
		for _, t := range s.Types {
			if slices.Contains(supported, t) && slices.Contains(archive.HandoffExtensions, t) {
				out = append(out, t)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("--dest %s: none of its configured types can take hand-offs on the %s backend", dest, cfg.LLM.Backend)
		}
		return out, nil
	}
	return nil, fmt.Errorf("--dest %s: not a configured sources[] directory", dest)
}

func ingestHostSetup(cmd *cobra.Command) (cfg *config.Config, root, dest string, types []string, err error) {
	configPath, err := resolveConfigPath(cmd)
	if err != nil {
		return nil, "", "", nil, err
	}
	if cfg, err = config.Load(configPath); err != nil {
		return nil, "", "", nil, err
	}
	root, _ = cmd.Flags().GetString("root")
	dest, _ = cmd.Flags().GetString("dest")
	if root, err = filepath.Abs(root); err != nil {
		return nil, "", "", nil, err
	}
	if dest, err = filepath.Abs(dest); err != nil {
		return nil, "", "", nil, err
	}
	types, err = ingestHostTypes(cfg, dest)
	return cfg, root, dest, types, err
}

func runArchivePickup(cmd *cobra.Command, _ []string) error {
	_, root, dest, types, err := ingestHostSetup(cmd)
	if err != nil {
		return err
	}
	res, err := archive.Pickup(root, dest, types)
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		return json.NewEncoder(w).Encode(res)
	}
	for _, r := range res {
		if r.Status == "refused" {
			fmt.Fprintf(w, "refused  %s  %s\n", r.CaptureID, r.Reason)
			continue
		}
		fmt.Fprintf(w, "%-8s %s  %s\n", r.Status, r.CaptureID, r.SourcePath)
	}
	return nil
}

func runArchiveReceipts(cmd *cobra.Command, _ []string) error {
	cfg, root, dest, _, err := ingestHostSetup(cmd)
	if err != nil {
		return err
	}
	reader, err := ingest.OpenLedgerReader(cfg.DBPath)
	if err != nil {
		return err
	}
	defer reader.Close()
	lookup := func(p, h string) (archive.LedgerEntry, bool, error) {
		e, found, err := reader.Lookup(p, h)
		return archive.LedgerEntry{Status: e.Status, WikiPage: e.WikiPage, DigestedAt: e.DigestedAt}, found, err
	}
	out, err := archive.WriteReceipts(root, dest, lookup, time.Now())
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "wrote %d receipt(s)\n", len(out))
	return nil
}
