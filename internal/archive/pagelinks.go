package archive

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// v3Tables hold the capture-to-page association (schema version 3).
// page_links is append-only: a row is added when a collected receipt
// observes that the ingest ledger recorded success with a wiki page for the
// capture's bytes, and it is never updated or removed by ingest.
const v3Tables = `
CREATE TABLE IF NOT EXISTS page_links (
 capture_id TEXT NOT NULL REFERENCES captures(capture_id),
 wiki_page TEXT NOT NULL,
 object_id TEXT NOT NULL REFERENCES objects(object_id),
 basis_receipt TEXT NOT NULL REFERENCES receipts(name),
 digested_at TEXT NOT NULL,
 observed_at TEXT NOT NULL,
 recorded_at TEXT NOT NULL,
 PRIMARY KEY (capture_id, wiki_page));
CREATE INDEX IF NOT EXISTS page_links_page ON page_links(wiki_page);
`

// PageLink is the archive's record that one capture is associated with one
// wiki source page. The archive registry is the authority for this
// association; the ingest host's ledger stays the authority for the ingest
// result, and BasisReceipt names the collected receipt (an observation of
// that ledger) the link was made from. A link never changes the wiki page,
// and it does not show that the page still exists, describes this edition,
// or supports any claim.
type PageLink struct {
	CaptureID    string `json:"capture_id"`
	WikiPage     string `json:"wiki_page"`
	ObjectID     string `json:"object_id"`
	ContentHash  string `json:"content_hash"`
	BasisReceipt string `json:"basis_receipt"`
	DigestedAt   string `json:"digested_at"`
	ObservedAt   string `json:"observed_at"`
	RecordedAt   string `json:"recorded_at"`
	// Current is true when the capture's latest collected receipt still
	// observes ledger status success with this page. A link stays recorded
	// when a later receipt observes something else.
	Current bool `json:"current"`
}

// receiptSeq is the per-capture sequence number in a receipt file name
// (<capture_id>.<n>.json). WriteReceipts gives each new observation of a
// capture the next number, so n is the observation order. It is the only
// order the archive uses for receipts: observed_at comes from the ingest
// host's clock, which can go backwards, and names sort .10 before .2.
// It returns 0 for a name that does not match.
func receiptSeq(name string) int {
	m := receiptNamePattern.FindStringSubmatch(name)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[2])
	return n
}

// receiptLess orders receipt names by capture ID, then sequence number.
func receiptLess(a, b string) bool {
	ma, mb := receiptNamePattern.FindStringSubmatch(a), receiptNamePattern.FindStringSubmatch(b)
	if ma == nil || mb == nil || ma[1] != mb[1] {
		return a < b
	}
	return receiptSeq(a) < receiptSeq(b)
}

// linkReceipt records the page link a receipt observes, if any. Only a
// success observation with a wiki page links. The basis of a (capture,
// page) link is the linking receipt with the lowest sequence number, in
// whatever order receipts are collected or backfilled. The receipt's
// object must be the capture's object.
func linkReceipt(ctx context.Context, q querier, name string, r Receipt, recordedAt string) error {
	if r.LedgerStatus != "success" || r.WikiPage == "" {
		return nil
	}
	if !ValidWikiPage(r.WikiPage) {
		return fmt.Errorf("%w: wiki_page %q is not a wiki-relative .md path", ErrInvalid, r.WikiPage)
	}
	var objectID string
	err := q.QueryRowContext(ctx, `SELECT object_id FROM captures WHERE capture_id = ?`, r.CaptureID).Scan(&objectID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: receipt %s names unknown capture %s", ErrInvalid, name, r.CaptureID)
	}
	if err != nil {
		return err
	}
	if objectID != r.ObjectID || r.ContentHash != strings.TrimPrefix(objectID, "sha256:") {
		return fmt.Errorf("%w: receipt %s object does not match capture %s", ErrInvalid, name, r.CaptureID)
	}
	res, err := q.ExecContext(ctx, `INSERT INTO page_links (capture_id, wiki_page, object_id, basis_receipt, digested_at, observed_at, recorded_at) VALUES (?,?,?,?,?,?,?) ON CONFLICT (capture_id, wiki_page) DO NOTHING`,
		r.CaptureID, r.WikiPage, objectID, name, r.DigestedAt, r.ObservedAt, recordedAt)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 1 {
		return err
	}
	var basis string
	if err := q.QueryRowContext(ctx, `SELECT basis_receipt FROM page_links WHERE capture_id = ? AND wiki_page = ?`, r.CaptureID, r.WikiPage).Scan(&basis); err != nil {
		return err
	}
	if receiptSeq(name) >= receiptSeq(basis) {
		return nil
	}
	_, err = q.ExecContext(ctx, `UPDATE page_links SET basis_receipt = ?, digested_at = ?, observed_at = ? WHERE capture_id = ? AND wiki_page = ?`,
		name, r.DigestedAt, r.ObservedAt, r.CaptureID, r.WikiPage)
	return err
}

// backfillPageLinks links every receipt already collected, in receipt
// sequence order (the basis rule makes the result independent of order).
// It runs when a version 2 registry (for example a restored backup) is
// upgraded; it is idempotent.
func backfillPageLinks(ctx context.Context, q querier) error {
	rows, err := q.QueryContext(ctx, `SELECT name, body, collected_at FROM receipts`)
	if err != nil {
		return err
	}
	type row struct{ name, body, collected string }
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.name, &r.body, &r.collected); err != nil {
			rows.Close()
			return err
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	sort.Slice(all, func(i, j int) bool { return receiptLess(all[i].name, all[j].name) })
	for _, r := range all {
		var rec Receipt
		if err := decodeStrict([]byte(r.body), &rec); err != nil {
			return fmt.Errorf("receipt %s: %w", r.name, err)
		}
		if err := linkReceipt(ctx, q, r.name, rec, r.collected); err != nil {
			return err
		}
	}
	return nil
}

// latestObservation returns the capture's collected receipt with the
// highest sequence number (receiptSeq), never the latest observed_at.
func (a *Archive) latestObservation(ctx context.Context, captureID string) (Receipt, bool, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT name, body FROM receipts WHERE capture_id = ?`, captureID)
	if err != nil {
		return Receipt{}, false, err
	}
	best, body := 0, ""
	for rows.Next() {
		var name, b string
		if err := rows.Scan(&name, &b); err != nil {
			rows.Close()
			return Receipt{}, false, err
		}
		if n := receiptSeq(name); n > best {
			best, body = n, b
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil || best == 0 {
		return Receipt{}, false, err
	}
	var r Receipt
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		return Receipt{}, false, err
	}
	return r, true, nil
}

// PageLinksFor lists the captures of active objects linked to a wiki page,
// in recording order. It is the full current list for the page; the
// archive_capture_ids frontmatter key is only a digest-time snapshot.
func (a *Archive) PageLinksFor(ctx context.Context, wikiPage string) ([]PageLink, error) {
	if !ValidWikiPage(wikiPage) {
		return nil, fmt.Errorf("%w: %q is not a wiki-relative .md path", ErrInvalid, wikiPage)
	}
	return a.pageLinks(ctx, pageLinksByPage, wikiPage)
}

// The page-link queries are whole constants: callers choose one, so no caller
// string reaches the SQL text.
const (
	pageLinksSelect    = `SELECT l.capture_id, l.wiki_page, l.object_id, l.basis_receipt, l.digested_at, l.observed_at, l.recorded_at FROM page_links l JOIN objects o ON o.object_id = l.object_id AND o.state = 'active' WHERE `
	pageLinksOrder     = ` ORDER BY l.recorded_at, l.capture_id, l.wiki_page`
	pageLinksByCapture = pageLinksSelect + `l.capture_id = ?` + pageLinksOrder
	pageLinksByPage    = pageLinksSelect + `l.wiki_page = ?` + pageLinksOrder
)

func (a *Archive) pageLinks(ctx context.Context, query string, arg string) ([]PageLink, error) {
	rows, err := a.db.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, err
	}
	out := []PageLink{}
	for rows.Next() {
		var l PageLink
		if err := rows.Scan(&l.CaptureID, &l.WikiPage, &l.ObjectID, &l.BasisReceipt, &l.DigestedAt, &l.ObservedAt, &l.RecordedAt); err != nil {
			rows.Close()
			return nil, err
		}
		l.ContentHash = strings.TrimPrefix(l.ObjectID, "sha256:")
		out = append(out, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		r, ok, err := a.latestObservation(ctx, out[i].CaptureID)
		if err != nil {
			return nil, err
		}
		out[i].Current = ok && r.LedgerStatus == "success" && r.WikiPage == out[i].WikiPage
	}
	return out, nil
}
