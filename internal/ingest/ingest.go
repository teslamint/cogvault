package ingest

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/adrg/frontmatter"

	"github.com/teslamint/cogvault/internal/config"
	cverr "github.com/teslamint/cogvault/internal/errors"
	"github.com/teslamint/cogvault/internal/index"
	"github.com/teslamint/cogvault/internal/llm"
	"github.com/teslamint/cogvault/internal/schema"
	"github.com/teslamint/cogvault/internal/storage"

	"golang.org/x/sys/unix"
)

const (
	maxAttempts  = 3
	maxFileSize  = 32 << 20
	settleWindow = 2 * time.Minute
)

// ErrAlreadyRunning is returned by Run when another ingest holds the flock.
var ErrAlreadyRunning = errors.New("ingest already running")

type RunOptions struct {
	DryRun bool
	Limit  int
	Origin string
}

type Runner struct {
	cfg    *config.Config
	store  storage.Storage
	idx    index.Index
	llm    llm.Adapter
	ledger *ledger
	dbPath string

	// injectable for tests; defaults set in New.
	settleWindow time.Duration
	maxFileSize  int64
	now          func() time.Time
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
		cfg:          cfg,
		store:        store,
		idx:          idx,
		llm:          llmAdapter,
		ledger:       l,
		dbPath:       dbPath,
		settleWindow: settleWindow,
		maxFileSize:  maxFileSize,
		now:          time.Now,
	}, nil
}

func (r *Runner) Close() error {
	return r.ledger.close()
}

func (r *Runner) Run(ctx context.Context, opts RunOptions) (*Report, error) {
	unlock, err := acquireLock(r.dbPath)
	if err != nil {
		return nil, err
	}
	defer unlock()

	report := &Report{}

	schemaText, err := r.readSchema()
	if err != nil {
		return report, err
	}

	digested := 0
	for _, entry := range r.scan(report) {
		if err := ctx.Err(); err != nil {
			return report, fmt.Errorf("ingest.Run: %w", err)
		}
		if opts.Limit > 0 && digested >= opts.Limit {
			break
		}

		hash := contentHash(entry.data)
		prev, found, err := r.ledger.lookup(entry.absPath, hash)
		if err != nil {
			return report, fmt.Errorf("ingest.Run: %w", err)
		}
		if found {
			switch prev.status {
			case "success":
				report.Unchanged++
				continue
			case "failed":
				if prev.attempts >= maxAttempts {
					report.Skipped++
					report.PerFile = append(report.PerFile, FileResult{
						Path: entry.absPath, Action: actionExhausted, Error: prev.lastError,
					})
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

	return report, nil
}

type scanEntry struct {
	absPath   string
	sourceDir string
	data      []byte
}

func (r *Runner) scan(report *Report) []scanEntry {
	var entries []scanEntry
	now := r.now()
	for _, src := range r.cfg.Sources {
		dir := filepath.Clean(src.Path)
		types := src.Types
		dirEntries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, de := range dirEntries {
			name := de.Name()
			abs := filepath.Join(dir, name)
			info, err := os.Lstat(abs)
			if err != nil {
				continue
			}
			if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
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
			data, err := os.ReadFile(abs)
			if err != nil {
				report.Skipped++
				report.PerFile = append(report.PerFile, FileResult{Path: abs, Action: actionSkipped, Error: "read: " + err.Error()})
				continue
			}
			entries = append(entries, scanEntry{absPath: abs, sourceDir: dir, data: data})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].absPath < entries[j].absPath })
	return entries
}

func (r *Runner) digestOne(ctx context.Context, entry scanEntry, hash, schemaText, origin string, prev *ledgerRow, report *Report) {
	slug := slugFor(entry.absPath, hash)
	res, err := r.llm.Digest(ctx, llm.DigestRequest{
		SourcePath: entry.absPath,
		SchemaText: schemaText,
		PageSlug:   slug,
	})
	if err != nil {
		r.recordFailure(entry, hash, origin, prev, report, "digest: "+err.Error(), errors.Is(err, llm.ErrTransient))
		return
	}

	fm, title, ok := parsePage(res.PageContent)
	if !ok {
		r.recordFailure(entry, hash, origin, prev, report, "validate: page missing frontmatter or title", false)
		return
	}

	page, err := r.pagePath(slug, entry.absPath)
	if err != nil {
		r.recordFailure(entry, hash, origin, prev, report, "ledger: "+err.Error(), false)
		return
	}

	if err := r.store.Write(page, []byte(res.PageContent)); err != nil {
		r.recordFailure(entry, hash, origin, prev, report, "write: "+err.Error(), false)
		return
	}

	if err := r.idx.Add(page, res.PageContent, buildMeta(fm, title)); err != nil {
		r.recordFailure(entry, hash, origin, prev, report, "index: "+err.Error(), false)
		return
	}

	if err := r.ledger.supersedePrevSuccess(entry.absPath); err != nil {
		r.recordFailure(entry, hash, origin, prev, report, "ledger: "+err.Error(), false)
		return
	}
	err = r.ledger.upsert(ledgerRow{
		sourcePath:  entry.absPath,
		contentHash: hash,
		sourceDir:   entry.sourceDir,
		digestedAt:  r.now().UTC().Format(time.RFC3339Nano),
		wikiPage:    page,
		status:      "success",
		attempts:    attemptsOf(prev),
		lastError:   "",
		runOrigin:   origin,
	})
	if err != nil {
		r.recordFailure(entry, hash, origin, prev, report, "ledger: "+err.Error(), false)
		return
	}

	report.Digested++
	report.PerFile = append(report.PerFile, FileResult{Path: entry.absPath, Action: actionDigested})
}

func (r *Runner) recordFailure(entry scanEntry, hash, origin string, prev *ledgerRow, report *Report, msg string, transient bool) {
	attempts := attemptsOf(prev)
	if !transient {
		attempts++
	}
	_ = r.ledger.upsert(ledgerRow{
		sourcePath:  entry.absPath,
		contentHash: hash,
		sourceDir:   entry.sourceDir,
		digestedAt:  r.now().UTC().Format(time.RFC3339Nano),
		wikiPage:    "",
		status:      "failed",
		attempts:    attempts,
		lastError:   msg,
		runOrigin:   origin,
	})
	report.Failed++
	report.PerFile = append(report.PerFile, FileResult{Path: entry.absPath, Action: actionFailed, Error: msg})
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
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '.' || ch == '_' || ch == '-' {
			b.WriteRune(ch)
		}
	}
	slug := collapseDashes(b.String())
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
	return map[string]string{
		"title": title,
		"type":  pageType,
		"tags":  joinTags(fm["tags"]),
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
