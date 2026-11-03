package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Attempt is one recovery attempt for a capture whose original is missing
// or incomplete. Attempts are append-only: a new attempt never changes the
// earlier capture, and a recovered original is a new capture linked here.
type Attempt struct {
	AttemptID       int64  `json:"attempt_id"`
	CaptureID       string `json:"capture_id"`
	Cause           string `json:"cause"`
	Tried           string `json:"tried"`
	Result          string `json:"result"`
	ResultCaptureID string `json:"result_capture_id,omitempty"`
	RemainingGap    string `json:"remaining_gap"`
	NextAction      string `json:"next_action"`
	RecordedAt      string `json:"recorded_at"`
}

var (
	attemptCauses  = []string{"citation_only", "truncated", "access_restricted", "missing_parts", "edition_mismatch", "extraction_failed", "other"}
	attemptResults = []string{"acquired", "partial", "failed", "human_action_required"}
)

// RecordAttempt appends one recovery attempt. An acquired or partial result
// must name the new source capture that holds what was recovered; the
// recovered capture keeps its own completeness, so recovery alone never
// makes anything verified.
func (a *Archive) RecordAttempt(ctx context.Context, at Attempt) (Attempt, error) {
	unlock, err := a.lock(false)
	if err != nil {
		return Attempt{}, err
	}
	defer unlock()
	if !oneOf(at.Cause, attemptCauses) {
		return Attempt{}, fmt.Errorf("%w: cause %q (want %s)", ErrInvalid, at.Cause, strings.Join(attemptCauses, ", "))
	}
	if !oneOf(at.Result, attemptResults) {
		return Attempt{}, fmt.Errorf("%w: result %q (want %s)", ErrInvalid, at.Result, strings.Join(attemptResults, ", "))
	}
	recovered := at.Result == "acquired" || at.Result == "partial"
	if recovered != (at.ResultCaptureID != "") {
		return Attempt{}, fmt.Errorf("%w: result_capture_id is required for acquired or partial and forbidden otherwise", ErrInvalid)
	}
	if strings.TrimSpace(at.Tried) == "" {
		return Attempt{}, fmt.Errorf("%w: tried must say what was tried", ErrInvalid)
	}
	if err := checkText(at.Tried, at.RemainingGap, at.NextAction); err != nil {
		return Attempt{}, err
	}
	tx, err := a.db.BeginTx(ctx, nil)
	if err != nil {
		return Attempt{}, err
	}
	defer tx.Rollback()
	if err := activeCapture(ctx, tx, at.CaptureID); err != nil {
		return Attempt{}, err
	}
	var result any
	if recovered {
		if at.ResultCaptureID == at.CaptureID {
			return Attempt{}, fmt.Errorf("%w: a recovery must produce a new capture", ErrInvalid)
		}
		if err := activeSource(ctx, tx, at.ResultCaptureID); err != nil {
			return Attempt{}, err
		}
		result = at.ResultCaptureID
	}
	at.RecordedAt = a.now().UTC().Format(time.RFC3339Nano)
	res, err := tx.ExecContext(ctx, `INSERT INTO attempts (capture_id, cause, tried, result, result_capture_id, remaining_gap, next_action, recorded_at) VALUES (?,?,?,?,?,?,?,?)`,
		at.CaptureID, at.Cause, at.Tried, at.Result, result, at.RemainingGap, at.NextAction, at.RecordedAt)
	if err != nil {
		if recovered {
			// attempts_link_once: this pair is already linked (possibly by a
			// concurrent run that committed first). The pool has one
			// connection, so the check must go through tx.
			var n int
			if tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempts WHERE capture_id = ? AND result_capture_id = ?`, at.CaptureID, at.ResultCaptureID).Scan(&n) == nil && n > 0 {
				return Attempt{}, fmt.Errorf("%w: %s is already linked to %s", ErrConflict, at.CaptureID, at.ResultCaptureID)
			}
		}
		return Attempt{}, err
	}
	if at.AttemptID, err = res.LastInsertId(); err != nil {
		return Attempt{}, err
	}
	return at, tx.Commit()
}

func (a *Archive) attempts(ctx context.Context, column, captureID string) ([]Attempt, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT attempt_id, capture_id, cause, tried, result, COALESCE(result_capture_id, ''), remaining_gap, next_action, recorded_at FROM attempts WHERE `+column+` = ? ORDER BY attempt_id`, captureID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attempt{}
	for rows.Next() {
		var at Attempt
		if err := rows.Scan(&at.AttemptID, &at.CaptureID, &at.Cause, &at.Tried, &at.Result, &at.ResultCaptureID, &at.RemainingGap, &at.NextAction, &at.RecordedAt); err != nil {
			return nil, err
		}
		out = append(out, at)
	}
	return out, rows.Err()
}

// reassessmentTried is the attempt text for Reassess. It names the actor
// and says the basis is the operator's own statement.
const reassessmentTried = "operator reassessment of the stored bytes; the basis is the operator's statement, not an independent check by cogvault"

// Reassess records an operator's judgment, with a stated basis, that the
// stored bytes of a held capture are complete. Cogvault does not check the
// basis against anything outside the archive. It never changes the held
// capture: it adds a new source capture of the same object with the same
// provenance (name, URL, title, media type, supplier), completeness
// verified, and the basis, and links the two with an attempt whose text
// names the operator. Only an acquired capture whose completeness is
// unknown qualifies; bytes recorded as incomplete cannot become verified,
// so a complete original must be captured as new bytes and linked with
// RecordAttempt instead.
//
// Reassess is safe to repeat: the same reassessment returns the same
// capture, links it if an earlier run stopped before linking, and never
// adds a second link.
func (a *Archive) Reassess(ctx context.Context, captureID, basis string) (Result, error) {
	if strings.TrimSpace(basis) == "" {
		return Result{}, fmt.Errorf("%w: a reassessment needs the checkable basis for verified", ErrInvalid)
	}
	held, err := a.Status(ctx, captureID)
	if err != nil {
		return Result{}, err
	}
	switch {
	case held.Kind != "source":
		return Result{}, fmt.Errorf("%w: %s is a %s, not a source capture", ErrInvalid, captureID, held.Kind)
	case held.Acquisition != "acquired" || held.Completeness == "incomplete":
		return Result{}, fmt.Errorf("%w: %s holds incomplete bytes; capture the complete original and link it with an attempt", ErrInvalid, captureID)
	case held.Completeness == "verified":
		return Result{}, fmt.Errorf("%w: %s is already verified", ErrInvalid, captureID)
	}
	f, err := os.OpenFile(a.objectPath(strings.TrimPrefix(held.ObjectID, "sha256:")), os.O_RDONLY|noFollow, 0)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %s: %v", ErrIntegrity, held.ObjectID, err)
	}
	defer f.Close()
	res, err := a.CaptureSource(ctx, SourceRequest{
		Content: f, OriginalName: held.OriginalName, URL: held.URL, Title: held.Title,
		MediaType: held.MediaType, Supplier: held.Supplier, Completeness: "verified", Basis: basis,
	})
	if err != nil {
		return Result{}, err
	}
	if res.ObjectID != held.ObjectID {
		return Result{}, fmt.Errorf("%w: reassessed bytes do not match %s", ErrIntegrity, held.ObjectID)
	}
	for _, at := range held.Attempts {
		if at.ResultCaptureID == res.CaptureID {
			return res, nil
		}
	}
	if _, err := a.RecordAttempt(ctx, Attempt{
		CaptureID: captureID, Cause: "other", Tried: reassessmentTried,
		Result: "acquired", ResultCaptureID: res.CaptureID, NextAction: "hand off the reassessed capture",
	}); err != nil && !errors.Is(err, ErrConflict) {
		return res, fmt.Errorf("reassessed capture %s was stored but not linked to %s; run the same reassessment again to link it: %w", res.CaptureID, captureID, err)
	}
	return res, nil
}

// activeCapture checks that id names any capture whose object is active.
func activeCapture(ctx context.Context, q querier, id string) error {
	var state string
	err := q.QueryRowContext(ctx, `SELECT o.state FROM captures c JOIN objects o ON o.object_id = c.object_id WHERE c.capture_id = ?`, id).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && state != "active") {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return err
}

func oneOf(v string, set []string) bool {
	for _, s := range set {
		if v == s {
			return true
		}
	}
	return false
}
