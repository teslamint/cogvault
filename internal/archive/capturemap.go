package archive

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

// The capture map ties copies in an ingest source directory to the archive
// captures they came from. Pickup writes it; ingest reads it to record the
// captures on the source page it writes. It lives in the source directory
// as a dot file no ingest type matches, and it is trusted exactly as far as
// the source directory is: whoever can write source files can write it.
// Ingest therefore records captures only when the map's object ID equals the
// SHA-256 of the bytes it digests, so a stale or edited entry for other
// bytes records nothing.

// CaptureMapName is the capture map's file name in a source directory.
const CaptureMapName = ".cogvault-archive-captures.json"

const maxCaptureMapSize = 16 << 20

// CaptureMapEntry is one copied object: its ID and every capture handed off
// with those bytes.
type CaptureMapEntry struct {
	ObjectID   string   `json:"object_id"`
	CaptureIDs []string `json:"capture_ids"`
}

type captureMap struct {
	Version int                        `json:"version"`
	Entries map[string]CaptureMapEntry `json:"entries"`
}

func readCaptureMap(dir string) (captureMap, error) {
	m := captureMap{Version: 1, Entries: map[string]CaptureMapEntry{}}
	data, err := readBounded(filepath.Join(dir, CaptureMapName), maxCaptureMapSize)
	if errors.Is(err, fs.ErrNotExist) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	if err := decodeStrict(data, &m); err != nil {
		return m, fmt.Errorf("capture map: %w", err)
	}
	if m.Version != 1 || m.Entries == nil {
		return m, fmt.Errorf("%w: capture map version or entries invalid", ErrInvalid)
	}
	return m, nil
}

// updateCaptureMap adds the picked-up captures to dir's map and replaces
// the map file atomically. Capture IDs only accumulate: a capture handed
// off for these bytes stays a capture of these bytes. An exclusive flock on
// dir serializes overlapping Pickups from read to rename, so neither loses
// the other's entries. A map that would exceed maxCaptureMapSize is refused
// before anything is written, so the map stays readable.
func updateCaptureMap(dir string, picked []PickupResult) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	for {
		err = unix.Flock(int(d.Fd()), unix.LOCK_EX)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("capture map lock: %w", err)
	}
	defer unix.Flock(int(d.Fd()), unix.LOCK_UN)
	m, err := readCaptureMap(dir)
	if err != nil {
		return err
	}
	changed := false
	for _, p := range picked {
		if p.Status == "refused" || p.SourcePath == "" {
			continue
		}
		name := filepath.Base(p.SourcePath)
		e := m.Entries[name]
		if e.ObjectID != "" && e.ObjectID != p.ObjectID {
			return fmt.Errorf("%w: capture map lists %s as %s, not %s", ErrConflict, name, e.ObjectID, p.ObjectID)
		}
		e.ObjectID = p.ObjectID
		if !oneOf(p.CaptureID, e.CaptureIDs) {
			e.CaptureIDs = append(e.CaptureIDs, p.CaptureID)
			sort.Strings(e.CaptureIDs)
			changed = true
		}
		m.Entries[name] = e
	}
	if !changed {
		return nil
	}
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if len(body) > maxCaptureMapSize {
		return fmt.Errorf("%w: capture map would be %d bytes; the limit is %d", ErrTooLarge, len(body), maxCaptureMapSize)
	}
	tmp, err := os.CreateTemp(dir, ".cogvault-archive-captures-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, CaptureMapName)); err != nil {
		return err
	}
	return syncDir(dir)
}

// CapturesForSource returns the captures recorded for a source file, but
// only if the map's object ID matches contentHash (the hex SHA-256 of the
// bytes being digested). A missing map, a missing entry, or a mismatch
// returns nothing; a malformed map is an error, which callers treat as
// nothing to record.
func CapturesForSource(sourcePath, contentHash string) (objectID string, captureIDs []string, err error) {
	m, err := readCaptureMap(filepath.Dir(sourcePath))
	if err != nil {
		return "", nil, err
	}
	e, ok := m.Entries[filepath.Base(sourcePath)]
	if !ok || e.ObjectID != "sha256:"+strings.ToLower(contentHash) || len(e.CaptureIDs) == 0 {
		return "", nil, nil
	}
	for i, id := range e.CaptureIDs {
		if !captureIDPattern.MatchString(id) || oneOf(id, e.CaptureIDs[:i]) {
			return "", nil, fmt.Errorf("%w: capture map has an invalid or repeated capture ID", ErrInvalid)
		}
	}
	return e.ObjectID, e.CaptureIDs, nil
}

// Frontmatter keys ingest writes on a source page from the capture map.
const (
	PageCaptureIDsKey = "archive_capture_ids"
	PageObjectIDKey   = "archive_object_id"
)

// PageRecordsCapture reports whether page frontmatter records captureID and
// objectID with the keys ingest writes. It reads only the given metadata.
func PageRecordsCapture(meta map[string]any, captureID, objectID string) bool {
	if obj, _ := meta[PageObjectIDKey].(string); obj != objectID {
		return false
	}
	ids, _ := meta[PageCaptureIDsKey].([]any)
	for _, v := range ids {
		if s, _ := v.(string); s == captureID {
			return true
		}
	}
	return false
}
