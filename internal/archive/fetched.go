package archive

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
	"time"
)

// FetchedRequest records the response body inside an existing `cogvault
// fetch` .md file as an original, and the .md file itself as a processed
// file derived from it. The caller splits the file and must prove the split
// exact (re-encoding gives the file's bytes back). The URL and fetch date
// come from the file's own frontmatter: they are what the file says, not an
// independent check of the HTTP exchange.
type FetchedRequest struct {
	Key string
	// Body is the response body as the fetch file holds it, byte for byte.
	Body []byte
	URL  string
	// FetchedAt is the fetch file's fetched_at value.
	FetchedAt string
	// DerivedName and Derived are the fetch file's base name and bytes.
	DerivedName string
	Derived     []byte
}

// Derivation is one processed file made from a capture's bytes. Its bytes
// are kept as their own object, so the file can be reproduced even after
// the copy in sources[] or the wiki moves or is deleted.
type Derivation struct {
	ObjectID   string `json:"object_id"`
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	AcquiredAt string `json:"acquired_at"`
	RecordedAt string `json:"recorded_at"`
}

const (
	// FetchedSupplier marks captures made by ImportFetched.
	FetchedSupplier = "imported cogvault fetch file (URL and date as written in the file)"
	// fetchedBodyExt is the original's extension. fetch does not record the
	// response media type and stores HTML as .md too, so the body's format
	// is unknown; this extension is no hand-off type, which keeps the body
	// held even after a reassessment.
	fetchedBodyExt = ".response"
)

// ImportFetched stores the body as a source capture (completeness unknown:
// a fetch cannot show the response was the whole document; name
// "<fetch file stem>.response", so it is never handed off as Markdown) and
// stores the fetch file as its own object, recorded as derived from that
// capture. Deleting the capture's object also quarantines or purges the
// derived object.
//
// Result boundary: every check that can refuse the import runs before the
// body is stored — field checks, the size limit (the derived file is
// staged first, so a file over MaxObjectBytes is refused here), and the
// deleted-object check for both inputs. The body capture and the
// derivation are still two commits. If the second fails (disk or database
// error), the body capture exists without its derivation and the error
// says so; repeating the same import returns that capture as existing and
// adds the derivation once.
func (a *Archive) ImportFetched(ctx context.Context, req FetchedRequest) (Result, error) {
	if len(req.Derived) == 0 || !bytes.HasSuffix(req.Derived, req.Body) {
		return Result{}, fmt.Errorf("%w: the fetch file must end with the body", ErrInvalid)
	}
	if req.URL == "" || strings.TrimSpace(req.FetchedAt) == "" {
		return Result{}, fmt.Errorf("%w: source_url and fetched_at are required", ErrInvalid)
	}
	if req.DerivedName == "" || path.Base(req.DerivedName) != req.DerivedName || strings.ContainsAny(req.DerivedName, `/\`) || strings.HasPrefix(req.DerivedName, ".") {
		return Result{}, fmt.Errorf("%w: derived name must be a base file name", ErrInvalid)
	}
	if err := checkText(req.FetchedAt, req.DerivedName); err != nil {
		return Result{}, err
	}
	staged, err := a.stage(bytes.NewReader(req.Derived))
	if err != nil {
		return Result{}, err
	}
	defer os.Remove(staged.tmpPath)
	if err := a.objectUsable(ctx, staged.objectID); err != nil {
		return Result{}, err
	}
	res, err := a.captureSource(ctx, SourceRequest{
		Key: req.Key, Content: bytes.NewReader(req.Body),
		OriginalName: strings.TrimSuffix(req.DerivedName, path.Ext(req.DerivedName)) + fetchedBodyExt,
		URL:          req.URL, Supplier: FetchedSupplier,
	}, req.FetchedAt, "")
	if err != nil {
		return Result{}, err
	}
	if err := a.addDerivation(ctx, staged, req, res.CaptureID); err != nil {
		return res, fmt.Errorf("body stored as %s but its derived file was not recorded; repeat the same import to record it: %w", res.CaptureID, err)
	}
	return res, nil
}

// objectUsable refuses an object that is quarantined or purged.
func (a *Archive) objectUsable(ctx context.Context, objectID string) error {
	var state string
	err := a.db.QueryRowContext(ctx, `SELECT state FROM objects WHERE object_id = ?`, objectID).Scan(&state)
	if err == nil && state != "active" {
		return fmt.Errorf("%w: %s is %s", ErrDeleted, objectID, state)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

func (a *Archive) addDerivation(ctx context.Context, staged stagedObject, req FetchedRequest, captureID string) error {
	unlock, err := a.lock(ctx, false)
	if err != nil {
		return err
	}
	defer unlock()
	// The body capture was committed before this lock; a quarantine or purge
	// may have run since. Record nothing for a deleted source.
	if err := activeCapture(ctx, a.db, captureID); err != nil {
		return err
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state string
	err = tx.QueryRowContext(ctx, `SELECT state FROM objects WHERE object_id = ?`, staged.objectID).Scan(&state)
	if err == nil && state != "active" {
		return fmt.Errorf("%w: derived %s is %s", ErrDeleted, staged.objectID, state)
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := a.publish(staged); err != nil {
		return err
	}
	now := a.now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO objects (object_id, size, stored_at) VALUES (?, ?, ?)`, staged.objectID, staged.size, now); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO derivations (derived_object_id, derived_name, kind, capture_id, acquired_at, recorded_at) VALUES (?,?,?,?,?,?)`,
		staged.objectID, req.DerivedName, "cogvault-fetch-md", captureID, req.FetchedAt, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (a *Archive) derivations(ctx context.Context, captureID string) ([]Derivation, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT d.derived_object_id, o.size, d.derived_name, d.kind, d.acquired_at, d.recorded_at FROM derivations d JOIN objects o ON o.object_id = d.derived_object_id WHERE d.capture_id = ? AND o.state = 'active' ORDER BY d.recorded_at, d.derived_object_id`, captureID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Derivation{}
	for rows.Next() {
		var d Derivation
		if err := rows.Scan(&d.ObjectID, &d.Size, &d.Name, &d.Kind, &d.AcquiredAt, &d.RecordedAt); err != nil {
			return nil, err
		}
		d.SHA256 = strings.TrimPrefix(d.ObjectID, "sha256:")
		out = append(out, d)
	}
	return out, rows.Err()
}

// derivedObjects returns the objects made from objectID's bytes (files
// recorded as derived from its captures and text extracted from it) that a
// deletion action moves together with objectID: they hold the same content,
// so keeping them would keep the deleted bytes. A derived object is never
// moved with another object when it is itself the original of a capture,
// or when another source it is derived from still needs it: for quarantine,
// another active source; for purge, another active or quarantined (still
// restorable) source. Restore brings back every derived object that is not
// a capture's original. Derived objects are never captures, so they have no
// derived objects of their own; one level is complete.
func (a *Archive) derivedObjects(ctx context.Context, objectID, action string) ([]string, error) {
	return a.ids(ctx, `WITH links(id, src) AS (
 SELECT d.derived_object_id, c.object_id FROM derivations d JOIN captures c ON c.capture_id = d.capture_id
 UNION SELECT x.output_object_id, x.source_object_id FROM extractions x WHERE x.output_object_id IS NOT NULL)
SELECT DISTINCT l.id FROM links l WHERE l.src = ?1 AND l.id <> ?1
 AND l.id NOT IN (SELECT object_id FROM captures)
 AND (?2 = 'restore' OR NOT EXISTS (
  SELECT 1 FROM links o JOIN objects s ON s.object_id = o.src
  WHERE o.id = l.id AND o.src <> ?1 AND (s.state = 'active' OR (?2 = 'purge' AND s.state = 'quarantined'))))
ORDER BY l.id`, objectID, action)
}
