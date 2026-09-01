package ingest

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/adrg/frontmatter"

	"github.com/teslamint/cogvault/internal/config"
	cverr "github.com/teslamint/cogvault/internal/errors"
	"github.com/teslamint/cogvault/internal/extract"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/llm"
	"github.com/teslamint/cogvault/internal/schema"
	"github.com/teslamint/cogvault/internal/storage"

	"golang.org/x/sys/unix"
	"golang.org/x/text/unicode/norm"
)

const (
	maxAttempts  = 3
	settleWindow = 2 * time.Minute
)

// ErrAlreadyRunning is returned by Run when another ingest holds the flock.
var ErrAlreadyRunning = errors.New("ingest already running")
var defaultNotify func(title, body string) error
// failureClass adjudicates whether a digest failure consumes a retry attempt.
// Only permanent (digest-output) problems increment attempts; transient LLM
// errors and infrastructure errors (write/index/ledger) are recorded as failed
// without consuming an attempt, so the file retries on the next run.
type failureClass int
const (
	classPermanent failureClass = iota // unparsable frontmatter / missing title
	classTransient                     // llm.ErrTransient
	classInfra                         // store.Write / idx.Add / ledger writes
	classRefused                       // llm.ErrRefused
)
type RunOptions struct {
	DryRun bool
	Limit  int
	Origin string
}
type Runner struct {
	cfg       *config.Config
	store     storage.Storage
	idx       index.Index
	llm       llm.Adapter
	extractor TextExtractor
	ledger    *ledger
	dbPath    string

	// injectable for tests; defaults set in New.
	settleWindow time.Duration
	maxFileSize  int64
	now          func() time.Time
	readDir      func(string) ([]os.DirEntry, error)
	notifyFunc   func(title, body string) error
	withTimeout  func(context.Context, time.Duration) (context.Context, context.CancelFunc)
}
type TextExtractor interface {
	Extract(context.Context, string) (string, error)
}
func New(cfg *config.Config, store storage.Storage, idx index.Index, llmAdapter llm.Adapter, dbPath string) (*Runner, error) {
	if cfg == nil {
		return nil, fmt.Errorf("ingest.New: nil config")
	}
	l, err := openLedger(dbPath)
	if err != nil {
		return nil, err
	}
	return &Runner{
		cfg: cfg, store: store, idx: idx, llm: llmAdapter,
		extractor: extract.NewPDFExtractor(cfg.LLM.MaxInputChars, extract.Commands{}),
		ledger:    l, dbPath: dbPath, settleWindow: settleWindow,
		maxFileSize: int64(cfg.MaxFileSizeMB) << 20, now: time.Now,
		readDir: os.ReadDir, notifyFunc: defaultNotify, withTimeout: context.WithTimeout,
	}, nil
}
// DigestProfile returns the retry identity for a digest configuration.
// ExtractionContractVersion is included because text-mode provider input is
// produced by the local extractor rather than sent as a raw PDF.
func DigestProfile(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	return fmt.Sprintf("%s|%s|%s|%d|%s", cfg.LLM.Backend, cfg.LLM.Model, strings.TrimRight(cfg.LLM.BaseURL, "/"), cfg.LLM.MaxInputChars, extract.ExtractionContractVersion)
}
func (r *Runner) digestProfile() string {
	return DigestProfile(r.cfg)
}
func (r *Runner) Close() error {
	return r.ledger.close()
}
func (r *Runner) Notify(report *Report) {
	if report == nil || len(report.NewAttention) == 0 {
		return
	}

	first := report.NewAttention[0]
	detail := first.Error
	if _, after, ok := strings.Cut(detail, ":"); ok {
		detail = after
	}
	detail = strings.TrimSpace(detail)
	runes := []rune(detail)
	if len(runes) > 60 {
		detail = string(runes[:60])
	}

	body := fmt.Sprintf("%d건 주의 필요 — %s (%s)", len(report.NewAttention), filepath.Base(first.Path), detail)
	if len(report.NewAttention) > 1 {
		body += fmt.Sprintf(" 외 %d건", len(report.NewAttention)-1)
	}
	if err := r.notifyFunc("cogvault ingest", body); err != nil {
		slog.Warn("ingest notification failed", "error", err)
	}
}
func (r *Runner) Run(ctx context.Context, opts RunOptions) (*Report, error) {
	unlock, err := acquireLock(r.dbPath)
	if err != nil {
		return nil, err
	}
	defer unlock()
	report := &Report{}
	if err := ctx.Err(); err != nil {
		return report, fmt.Errorf("ingest.Run: %w", err)
	}
	if err := r.sweepOrphans(ctx, report, opts.DryRun); err != nil {
		return report, fmt.Errorf("ingest.Run: %w", err)
	}
	schemaText, err := r.readSchema()
	if err != nil {
		return report, err
	}
	digested := 0
	entries := r.scan(report)
	currentProfile := r.digestProfile()
	for i, entry := range entries {
		if err := ctx.Err(); err != nil {
			return report, fmt.Errorf("ingest.Run: %w", err)
		}
		if opts.Limit > 0 && digested >= opts.Limit {
			report.NotExamined = len(entries) - i
			break
		}
		hash := entry.hash
		prev, found, err := r.ledger.lookup(entry.absPath, hash)
		if err != nil {
			return report, fmt.Errorf("ingest.Run: %w", err)
		}
		if found {
			switch prev.status {
			case "success":
				if err := r.handleSuccessRow(entry.absPath, prev, report); err != nil {
					if errors.Is(err, cverr.ErrNotFound) {
						break
					}
					continue
				}
				report.Unchanged++
				continue
			case "refused":
				if prev.digestProfile == currentProfile {
					report.Skipped++
					report.PerFile = append(report.PerFile, FileResult{Path: entry.absPath, Action: actionRefused, Error: prev.lastError})
					continue
				}
			case "failed":
				if prev.attempts >= maxAttempts && prev.digestProfile == currentProfile {
					report.Skipped++
					report.PerFile = append(report.PerFile, FileResult{Path: entry.absPath, Action: actionExhausted, Error: prev.lastError})
					continue
				}
			}
		}
		digested++
		if opts.DryRun {
			report.Digested++
			report.PerFile = append(report.PerFile, FileResult{Path: entry.absPath, Action: actionWouldDigest})
			continue
		}
		r.digestOne(ctx, entry, hash, schemaText, opts.Origin, prev, report)
	}
	if err := report.SumCheck(); err != nil {
		report.SumMismatch = err.Error()
	}
	return report, nil
}
func (r *Runner) handleSuccessRow(sourcePath string, prev *ledgerRow, report *Report) error {
	_, _, err := r.store.Stat(prev.wikiPage)
	if err == nil {
		return nil
	}
	if errors.Is(err, cverr.ErrNotFound) {
		return err
	}
	report.Failed++
	report.PerFile = append(report.PerFile, FileResult{
		Path:   sourcePath,
		Action: actionFailed,
		Error:  "stat wiki page: " + err.Error(),
	})
	return err
}
type scanEntry struct {
	absPath   string
	sourceDir string
	hash      string
	size      int64
	mtime     time.Time
}
func (r *Runner) scan(report *Report) []scanEntry {
	var entries []scanEntry
	now := r.now()
	for _, src := range r.cfg.Sources {
		dir := filepath.Clean(src.Path)
		types := src.Types
		dirEntries, err := os.ReadDir(dir)
		if err != nil {
			report.SourceErrors++
			report.PerFile = append(report.PerFile, FileResult{Path: dir, Action: actionSourceError, Error: sourceErrorText(err)})
			continue
		}
		for _, de := range dirEntries {
			name := de.Name()
			abs := filepath.Join(dir, name)
			info, err := os.Lstat(abs)
			if err != nil {
				report.Scanned++
				report.Skipped++
				report.PerFile = append(report.PerFile, FileResult{Path: abs, Action: actionSkipped, Error: "stat: " + err.Error()})
				continue
			}
			if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			report.Scanned++
			ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
			if !containsType(types, ext) {
				report.Skipped++
				report.PerFile = append(report.PerFile, FileResult{Path: abs, Action: actionSkipped, Error: "type not allowed"})
				continue
			}
			if info.Size() > r.maxFileSize {
				report.Skipped++
				report.PerFile = append(report.PerFile, FileResult{Path: abs, Action: actionSkipped, Error: "exceeds max file size"})
				continue
			}
			if now.Sub(info.ModTime()) < r.settleWindow {
				report.Deferred++
				report.PerFile = append(report.PerFile, FileResult{Path: abs, Action: actionDeferred, Error: "within settle window"})
				continue
			}
			// TOCTOU: file content may change between hashFile and the later LLM
			// read. Accepted for single-user local use — not worth locking.
			hash, err := hashFile(abs)
			if err != nil {
				report.Skipped++
				report.PerFile = append(report.PerFile, FileResult{Path: abs, Action: actionSkipped, Error: "read: " + sourceErrorText(err)})
				continue
			}
			entries = append(entries, scanEntry{absPath: abs, sourceDir: dir, hash: hash, size: info.Size(), mtime: info.ModTime()})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].absPath < entries[j].absPath })
	return entries
}
func (r *Runner) digestOne(parent context.Context, entry scanEntry, hash, schemaText, origin string, prev *ledgerRow, report *Report) {
	budget := 5 * time.Minute
	if r.cfg.LLM.TimeoutSeconds > 0 {
		budget = time.Duration(r.cfg.LLM.TimeoutSeconds) * time.Second
	}
	ctx, cancel := r.withTimeout(parent, budget)
	defer cancel()
	sourceText := ""
	if r.llm.InputMode() == llm.TextInput {
		var err error
		sourceText, err = r.extractor.Extract(ctx, entry.absPath)
		if err != nil {
			class := classPermanent
			if errors.Is(err, extract.ErrTransient) {
				class = classTransient
			}
			r.recordFailure(entry, hash, origin, prev, report, "extract: "+err.Error(), class)
			return
		}
	}
	slug := slugFor(entry.absPath, hash)
	res, err := r.llm.Digest(ctx, llm.DigestRequest{SourcePath: entry.absPath, SourceText: sourceText, SchemaText: schemaText, PageSlug: slug, SourceExt: filepath.Ext(entry.absPath)})
	if err != nil {
		class := classPermanent
		if errors.Is(err, llm.ErrTransient) {
			class = classTransient
		} else if errors.Is(err, llm.ErrRefused) {
			class = classRefused
		}
		r.recordFailure(entry, hash, origin, prev, report, "digest: "+err.Error(), class)
		return
	}
	fm, title, ok := parsePage(res.PageContent)
	if !ok {
		r.recordFailure(entry, hash, origin, prev, report, "validate: page missing frontmatter or title", classPermanent)
		return
	}
	page, err := r.pagePath(slug, entry.absPath)
	if err != nil {
		r.recordFailure(entry, hash, origin, prev, report, "ledger: "+err.Error(), classInfra)
		return
	}
	if err := r.store.Write(page, []byte(res.PageContent)); err != nil {
		r.recordFailure(entry, hash, origin, prev, report, "write: "+err.Error(), classInfra)
		return
	}
	if err := r.idx.Add(page, res.PageContent, buildMeta(fm, title)); err != nil {
		r.recordFailure(entry, hash, origin, prev, report, "index: "+err.Error(), classInfra)
		return
	}
	if err := r.ledger.supersedePrevSuccess(entry.absPath); err != nil {
		r.recordFailure(entry, hash, origin, prev, report, "ledger: "+err.Error(), classInfra)
		return
	}
	if err := r.ledger.upsert(ledgerRow{sourcePath: entry.absPath, contentHash: hash, sourceDir: entry.sourceDir, digestedAt: r.now().UTC().Format(time.RFC3339Nano), wikiPage: page, status: "success", runOrigin: origin, llmModel: r.cfg.LLM.Model, digestProfile: r.digestProfile()}); err != nil {
		r.recordFailure(entry, hash, origin, prev, report, "ledger: "+err.Error(), classInfra)
		return
	}
	report.Digested++
	report.PerFile = append(report.PerFile, FileResult{Path: entry.absPath, Action: actionDigested})
}
func (r *Runner) recordFailure(entry scanEntry, hash, origin string, prev *ledgerRow, report *Report, msg string, class failureClass) {
	attempts := attemptsOf(prev)
	if class == classPermanent {
		attempts++
	}
	status := "failed"
	if class == classRefused {
		status = "refused"
	}
	if err := r.ledger.upsert(ledgerRow{sourcePath: entry.absPath, contentHash: hash, sourceDir: entry.sourceDir, digestedAt: r.now().UTC().Format(time.RFC3339Nano), status: status, attempts: attempts, lastError: msg, runOrigin: origin, llmModel: r.cfg.LLM.Model, digestProfile: r.digestProfile()}); err != nil {
		slog.Error("recordFailure: ledger upsert", "path", entry.absPath, "error", err)
	}
	profile := r.digestProfile()
	legacy := prev != nil && prev.digestProfile == "" && prev.llmModel == r.cfg.LLM.Model
	if class == classPermanent && attempts >= maxAttempts && (prev == nil || (!legacy && (prev.digestProfile != profile || prev.attempts < maxAttempts)) || (legacy && prev.attempts < maxAttempts)) {
		report.NewAttention = append(report.NewAttention, FileResult{Path: entry.absPath, Action: actionFailed, Error: msg})
	}
	if class == classRefused && (prev == nil || (!legacy && (prev.status != "refused" || prev.digestProfile != profile))) {
		report.NewAttention = append(report.NewAttention, FileResult{Path: entry.absPath, Action: actionRefused, Error: msg})
	}
	if class == classRefused {
		report.Refused++
		report.PerFile = append(report.PerFile, FileResult{Path: entry.absPath, Action: actionRefused, Error: msg})
		return
	}
	report.Failed++
	report.PerFile = append(report.PerFile, FileResult{Path: entry.absPath, Action: actionFailed, Error: msg})
}
func (r *Runner) sweepOrphans(ctx context.Context, report *Report, dryRun bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	rows, err := r.ledger.successRows()
	if err != nil {
		slog.Warn("sweep: ledger query failed", "error", err)
		return err
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].sourceDir == rows[j].sourceDir {
			return rows[i].sourcePath < rows[j].sourcePath
		}
		return rows[i].sourceDir < rows[j].sourceDir
	})

	rowsByDir := make(map[string][]ledgerRow, len(rows))
	for _, row := range rows {
		dir := filepath.Clean(row.sourceDir)
		rowsByDir[dir] = append(rowsByDir[dir], row)
	}

	seenDirs := map[string]struct{}{}
	for _, src := range r.cfg.Sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		dir := filepath.Clean(src.Path)
		if _, seen := seenDirs[dir]; seen {
			continue
		}
		seenDirs[dir] = struct{}{}

		dirRows := rowsByDir[dir]
		if len(dirRows) == 0 {
			continue
		}

		snapshot, ok, err := r.snapshotDir(dir)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			r.reportSweepSourceError(report, dir, err)
			continue
		}
		if !ok {
			continue
		}
		candidates, survivors := orphanCandidates(dirRows, snapshot)
		if survivors < 1 || len(candidates) != 1 {
			continue
		}
		row := candidates[0]

		if dryRun {
			report.Archived++
			report.PerFile = append(report.PerFile, FileResult{
				Path: row.sourcePath, Action: actionWouldArchive,
			})
			continue
		}

		if err := ctx.Err(); err != nil {
			return err
		}
		recheck, ok, err := r.snapshotDir(dir)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			r.reportSweepSourceError(report, dir, err)
			continue
		}
		if !ok {
			continue
		}
		recheckCandidates, recheckSurvivors := orphanCandidates(dirRows, recheck)
		if recheckSurvivors < 1 || len(recheckCandidates) != 1 || !sameLedgerRow(row, recheckCandidates[0]) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		dst := archivedWikiPath(row)
		moveErr := r.store.Move(row.wikiPage, dst)
		if moveErr != nil && !errors.Is(moveErr, cverr.ErrNotFound) {
			slog.Warn("sweep: move failed", "src", row.wikiPage, "dst", dst, "error", moveErr)
			continue
		}

		row.status = "superseded"
		if err := r.ledger.upsert(row); err != nil {
			slog.Warn("sweep: ledger update failed", "path", row.sourcePath, "error", err)
			continue
		}

		report.Archived++
		report.PerFile = append(report.PerFile, FileResult{
			Path: row.sourcePath, Action: actionArchived,
		})
	}
	return nil
}
func (r *Runner) reportSweepSourceError(report *Report, dir string, err error) {
	slog.Warn("sweep: source dir snapshot failed, skipping", "dir", dir, "error", err)
	report.SourceErrors++
	report.PerFile = append(report.PerFile, FileResult{
		Path:   dir,
		Action: actionSourceError,
		Error:  sourceErrorText(err),
	})
}
type sourceSnapshot map[string]struct{}
func (r *Runner) snapshotDir(dir string) (sourceSnapshot, bool, error) {
	entries, err := r.readDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			slog.Warn("sweep: source dir unavailable, skipping", "dir", dir, "error", err)
			return nil, false, nil
		}
		return nil, false, err
	}
	snapshot := make(sourceSnapshot, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		snapshot[filepath.Join(dir, entry.Name())] = struct{}{}
	}
	return snapshot, true, nil
}
func orphanCandidates(rows []ledgerRow, snapshot sourceSnapshot) ([]ledgerRow, int) {
	candidates := make([]ledgerRow, 0, len(rows))
	survivors := 0
	for _, row := range rows {
		if _, ok := snapshot[row.sourcePath]; ok {
			survivors++
			continue
		}
		candidates = append(candidates, row)
	}
	return candidates, survivors
}
func sameLedgerRow(left, right ledgerRow) bool {
	return left.sourcePath == right.sourcePath &&
		left.contentHash == right.contentHash &&
		left.sourceDir == right.sourceDir &&
		left.wikiPage == right.wikiPage
}
func archivedWikiPath(row ledgerRow) string {
	base := filepath.Base(row.wikiPage)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)
	return "sources/_archived/" + name + "-" + row.contentHash[:8] + ext
}
func (r *Runner) pagePath(slug, absSourcePath string) (string, error) {
	base := "sources/" + slug + ".md"
	taken, err := r.ledger.wikiPageTakenByOther(base, absSourcePath)
	if err != nil {
		return "", err
	}
	if taken {
		return "sources/" + slug + "-" + hash8([]byte(absSourcePath)) + ".md", nil
	}
	return base, nil
}
func (r *Runner) readSchema() (string, error) {
	data, err := r.store.Read(r.cfg.SchemaPath())
	if err != nil {
		if errors.Is(err, cverr.ErrNotFound) {
			return schema.DefaultContent, nil
		}
		return "", fmt.Errorf("ingest.Run: %w", err)
	}
	return string(data), nil
}
func attemptsOf(prev *ledgerRow) int {
	if prev == nil {
		return 0
	}
	return prev.attempts
}
func contentHash(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)
}
// hashFile streams the file into a sha256 hasher so full contents are never
// retained in memory. Only the hex digest is kept per scan entry.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
func sourceErrorText(err error) string {
	if !errors.Is(err, fs.ErrPermission) {
		return err.Error()
	}
	msg := "permission denied: cannot read source"
	if runtime.GOOS == "darwin" {
		msg += `; macOS consent required, see README "Schedule zero-touch ingest"`
	}
	return msg
}
func hash8(data []byte) string {
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h)[:8]
}
func containsType(types []string, ext string) bool {
	for _, t := range types {
		if t == ext {
			return true
		}
	}
	return false
}
func slugFor(absPath, hash string) string {
	base := filepath.Base(absPath)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	base = strings.ToLower(base)
	base = strings.ReplaceAll(base, " ", "-")

	var b strings.Builder
	for _, ch := range base {
		if unicode.IsLetter(ch) || unicode.IsDigit(ch) || ch == '.' || ch == '_' || ch == '-' {
			b.WriteRune(ch)
		}
	}
	slug := collapseDashes(b.String())
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "src-" + hash[:8]
	}
	// iCloud Drive converts filenames from NFC to NFD, which can expand
	// Korean characters 2-3x in byte length (e.g. 126 → 276 bytes).
	// The filesystem filename limit is 255 bytes. Cap the slug so the
	// NFD form of "sources/<slug>-<hash8>.md" stays safely under 255.
	// Budget: 255 - len("sources/") - len("-") - 8 - len(".md") = 236.
	slug = truncateNFDSafe(slug, 220)
	slug = strings.Trim(slug, "-")
	if slug == "" {
		return "src-" + hash[:8]
	}
	return slug
}
func collapseDashes(s string) string {
	var b strings.Builder
	prevDash := false
	for _, ch := range s {
		if ch == '-' {
			if prevDash {
				continue
			}
			prevDash = true
		} else {
			prevDash = false
		}
		b.WriteRune(ch)
	}
	return b.String()
}
// truncateNFDSafe truncates s (NFC) so its NFD byte representation does not
// exceed maxBytes, without splitting a multi-byte character.
func truncateNFDSafe(s string, maxBytes int) string {
	nfd := norm.NFD.String(s)
	if len(nfd) <= maxBytes {
		return s
	}
	// Walk rune-by-rune through the NFC string, accumulating NFD byte cost.
	var nfcCut int
	var nfdLen int
	for i, r := range s {
		rNFD := norm.NFD.String(string(r))
		if nfdLen+len(rNFD) > maxBytes {
			nfcCut = i
			break
		}
		nfdLen += len(rNFD)
		nfcCut = i + utf8.RuneLen(r)
	}
	return s[:nfcCut]
}
// ErrAlreadyRunning is returned by Run when another ingest holds the flock.
// failureClass adjudicates whether a digest failure consumes a retry attempt.
// Only permanent (digest-output) problems increment attempts; transient LLM
// errors and infrastructure errors (write/index/ledger) are recorded as failed
// without consuming an attempt, so the file retries on the next run.
// DigestProfile returns the retry identity for a digest configuration.
// ExtractionContractVersion is included because text-mode provider input is
// produced by the local extractor rather than sent as a raw PDF.
// hashFile streams the file into a sha256 hasher so full contents are never
// retained in memory. Only the hex digest is kept per scan entry.

func parsePage(content string) (map[string]any, string, bool) {
	var fm map[string]any
	_, err := frontmatter.Parse(strings.NewReader(content), &fm)
	if err != nil || len(fm) == 0 {
		return nil, "", false
	}
	titleVal, ok := fm["title"]
	if !ok {
		return nil, "", false
	}
	title := strings.TrimSpace(fmt.Sprint(titleVal))
	if title == "" {
		return nil, "", false
	}
	return fm, title, true
}

func buildMeta(fm map[string]any, title string) map[string]string {
	pageType, _ := fm["type"].(string)
	category, _ := fm["category"].(string)
	return map[string]string{
		"title":    title,
		"type":     pageType,
		"category": index.NormalizeCategory(category),
		"tags":     joinTags(fm["tags"]),
	}
}

func joinTags(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, ",")
	case []string:
		return strings.Join(t, ",")
	default:
		return ""
	}
}

func acquireLock(dbPath string) (func(), error) {
	lockPath := filepath.Join(filepath.Dir(dbPath), "ingest.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("ingest.Run: open lock %s: %w", lockPath, err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrAlreadyRunning
		}
		return nil, fmt.Errorf("ingest.Run: flock %s: %w", lockPath, err)
	}
	return func() {
		unix.Flock(int(f.Fd()), unix.LOCK_UN)
		f.Close()
	}, nil
}
