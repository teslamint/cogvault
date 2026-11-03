package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/teslamint/cogvault/internal/archive"
)

// newArchiveOperatorCmds are the local operator actions of ADR 0027 items
// 11 and 13. They exist only on the command line: no MCP tool reaches them.
func newArchiveOperatorCmds() []*cobra.Command {
	impact := &cobra.Command{
		Use:   "impact <capture-or-object-id>",
		Short: "Report what depends on an object before a deletion request (changes nothing)",
		Args:  cobra.ExactArgs(1),
		RunE:  runArchiveImpact,
	}
	quarantine := &cobra.Command{
		Use:   "quarantine <capture-or-object-id>",
		Short: "Move an object to quarantine; it stays restorable for at least 30 days",
		Args:  cobra.ExactArgs(1),
		RunE:  runArchiveQuarantine,
	}
	quarantine.Flags().String("impact", "", "digest of the impact report you reviewed (required)")
	_ = quarantine.MarkFlagRequired("impact")
	unquarantine := &cobra.Command{
		Use:   "unquarantine <capture-or-object-id>",
		Short: "Restore a quarantined object",
		Args:  cobra.ExactArgs(1),
		RunE:  runArchiveUnquarantine,
	}
	purge := &cobra.Command{
		Use:   "purge <object-id>",
		Short: "Permanently remove one quarantined object after its 30-day quarantine",
		Args:  cobra.ExactArgs(1),
		RunE:  runArchivePurge,
	}
	purge.Flags().String("confirm", "", "repeat the exact object ID to confirm this one purge (required)")
	_ = purge.MarkFlagRequired("confirm")
	backup := &cobra.Command{
		Use:   "backup",
		Short: "Write a consistent backup (objects, registry copy, deletion records) into a new directory",
		Args:  cobra.NoArgs,
		RunE:  runArchiveBackup,
	}
	backup.Flags().String("to", "", "new backup directory (required; must not exist)")
	_ = backup.MarkFlagRequired("to")
	restoreTest := &cobra.Command{
		Use:   "restore-test",
		Short: "Restore a backup into a scratch directory, re-apply deletions, verify, and write a report",
		Args:  cobra.NoArgs,
		RunE:  runArchiveRestoreTest,
	}
	restoreTest.Flags().String("backup", "", "backup directory to test (required)")
	restoreTest.Flags().String("report", "", "report file to write (required; must not exist)")
	restoreTest.Flags().String("scratch", "", "parent directory for the scratch restore (default: system temp)")
	restoreTest.Flags().StringArray("deletion-log", nil, "deletion log to re-apply, such as the off-NAS replica (repeatable; default: this root's live log only, which does not show recovery after losing the archive host)")
	_ = restoreTest.MarkFlagRequired("backup")
	_ = restoreTest.MarkFlagRequired("report")
	reassess := &cobra.Command{
		Use:   "reassess <capture-id>",
		Short: "Record an operator's verified judgment of a held capture's stored bytes as a new linked capture",
		Long: "reassess records that you, the operator, judged the stored bytes complete, with --basis as your stated\n" +
			"evidence. cogvault does not check the basis against anything outside the archive. The held capture is\n" +
			"not changed; a new verified capture of the same bytes and provenance is linked to it by an attempt.",
		Args: cobra.ExactArgs(1),
		RunE: runArchiveReassess,
	}
	reassess.Flags().String("basis", "", "your checkable evidence that the bytes are complete (required; not verified by cogvault)")
	_ = reassess.MarkFlagRequired("basis")
	importFetched := &cobra.Command{
		Use:   "import-fetched",
		Short: "Store the response body inside an existing cogvault fetch .md file as an original, linked to that file",
		Args:  cobra.NoArgs,
		RunE:  runArchiveImportFetched,
	}
	importFetched.Flags().String("file", "", "fetch .md file written by cogvault fetch (required; read only)")
	importFetched.Flags().String("key", "", "idempotency key (default: the request digest)")
	_ = importFetched.MarkFlagRequired("file")
	acquire := &cobra.Command{
		Use:   "acquire [<public http(s) URL>]",
		Short: "Download one public URL as a new original (no credentials, cookies, queries, or private addresses)",
		Long: "acquire downloads one URL and stores the bytes exactly as sent, with the response provenance.\n" +
			"It refuses URLs with credentials, a query, or a fragment, and any host whose DNS answer includes a\n" +
			"non-public address, on every redirect. Completeness is always unknown. With --recover it downloads\n" +
			"an existing capture's URL again and records the result as a recovery attempt.",
		Args: cobra.MaximumNArgs(1),
		RunE: runArchiveAcquire,
	}
	acquire.Flags().String("recover", "", "capture ID whose URL to download again as a recovery attempt")
	acquire.Flags().String("cause", "citation_only", "attempt cause with --recover")
	extractCmd := &cobra.Command{
		Use:   "extract <capture-id>",
		Short: "Extract text from an HTML capture into a separate object (" + archive.HTMLTextExtractor + ")",
		Args:  cobra.ExactArgs(1),
		RunE:  runArchiveExtract,
	}
	return []*cobra.Command{impact, quarantine, unquarantine, purge, backup, restoreTest, reassess, importFetched, acquire, extractCmd}
}

func resolveObject(cmd *cobra.Command, a *archive.Archive, id string) (string, error) {
	return a.ResolveObject(cmd.Context(), id)
}

func runArchiveImpact(cmd *cobra.Command, args []string) error {
	a, err := openArchive(cmd)
	if err != nil {
		return err
	}
	defer a.Close()
	obj, err := resolveObject(cmd, a, args[0])
	if err != nil {
		return err
	}
	im, err := a.ImpactOf(cmd.Context(), obj)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(im)
}

func runArchiveQuarantine(cmd *cobra.Command, args []string) error {
	a, err := openArchive(cmd)
	if err != nil {
		return err
	}
	defer a.Close()
	obj, err := resolveObject(cmd, a, args[0])
	if err != nil {
		return err
	}
	digest, _ := cmd.Flags().GetString("impact")
	if err := a.Quarantine(cmd.Context(), obj, digest); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "quarantined %s (restorable for at least %s; nothing purges it automatically)\n", obj, archive.QuarantineMinimum)
	return nil
}

func runArchiveUnquarantine(cmd *cobra.Command, args []string) error {
	a, err := openArchive(cmd)
	if err != nil {
		return err
	}
	defer a.Close()
	obj, err := resolveObject(cmd, a, args[0])
	if err != nil {
		return err
	}
	if err := a.Unquarantine(cmd.Context(), obj); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "restored %s\n", obj)
	return nil
}

func runArchivePurge(cmd *cobra.Command, args []string) error {
	a, err := openArchive(cmd)
	if err != nil {
		return err
	}
	defer a.Close()
	confirm, _ := cmd.Flags().GetString("confirm")
	if err := a.Purge(cmd.Context(), args[0], confirm); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "purged %s; copies outside this archive (backups, ingest-host copies) are not removed by this command\n", args[0])
	return nil
}

func runArchiveBackup(cmd *cobra.Command, _ []string) error {
	a, err := openArchive(cmd)
	if err != nil {
		return err
	}
	defer a.Close()
	to, _ := cmd.Flags().GetString("to")
	if to, err = filepath.Abs(to); err != nil {
		return err
	}
	m, err := a.Backup(cmd.Context(), to)
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "backup %s: %d files\n", to, len(m.Files))
	return nil
}

// runArchiveRestoreTest restores a backup with the root's live deletion log
// re-applied, so a deletion made after the backup still holds. It exits
// non-zero when the report did not pass.
func runArchiveRestoreTest(cmd *cobra.Command, _ []string) error {
	root, _ := cmd.Flags().GetString("root")
	backupDir, _ := cmd.Flags().GetString("backup")
	report, _ := cmd.Flags().GetString("report")
	scratch, _ := cmd.Flags().GetString("scratch")
	var err error
	for _, p := range []*string{&root, &backupDir, &report} {
		if *p, err = filepath.Abs(*p); err != nil {
			return err
		}
	}
	logs, _ := cmd.Flags().GetStringArray("deletion-log")
	if len(logs) == 0 {
		logs = []string{filepath.Join(root, "deletions", "log.jsonl")}
		fmt.Fprintln(cmd.ErrOrStderr(), "note: using only this root's live deletion log; pass --deletion-log <replica> to test recovery without the archive host")
	}
	for i := range logs {
		if logs[i], err = filepath.Abs(logs[i]); err != nil {
			return err
		}
	}
	rep, err := archive.RestoreTest(cmd.Context(), backupDir, scratch, logs, time.Now())
	if err != nil {
		return err
	}
	body, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(report, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("--report: %w", err)
	}
	if _, err := f.Write(append(body, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if !rep.Passed {
		return fmt.Errorf("restore test did not pass; see %s", report)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "restore test passed: %d objects verified, %d deletion records re-applied\n", rep.ObjectsVerified, rep.DeletionsReapplied)
	return nil
}
