package trial

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// MarkerName marks a directory as a trial root. A root that has content and
	// no marker is never used, which is what keeps the trial off a production
	// archive or a wiki.
	MarkerName = ".cogvault-trial.json"
	// PauseName is a file whose presence pauses the worker without stopping it.
	PauseName = "PAUSE"
	// MarkerMode is the only mode a trial marker may name.
	MarkerMode = "no-backup-trial"
)

// productionArtifacts are names that mean a directory belongs to, or sits
// inside, a production archive or wiki: a trial root must not be one, hold one,
// or have one in a parent directory.
var productionArtifacts = []string{"registry.db", "archive.lock", "objects", ".cogvault.db", "config.yaml", "_wiki", ".obsidian"}

// ErrRoot is a refused trial root.
var ErrRoot = errors.New("trial root refused")

func rootErr(why string) error { return fmt.Errorf("%w: %s", ErrRoot, why) }

// Marker is the content of MarkerName.
type Marker struct {
	Mode       string `json:"mode"`
	ID         string `json:"id"`
	CreatedAt  string `json:"created_at"`
	AcceptedBy string `json:"accepted_by"`
}

// CheckRoot verifies, without creating anything, that the root may be used. The
// error text never names a path other than what the operator configured.
func (c Config) CheckRoot() error {
	info, err := os.Lstat(c.Root)
	exists := err == nil
	switch {
	case exists && info.Mode()&os.ModeSymlink != 0:
		return rootErr("the root is a symbolic link")
	case exists && !info.IsDir():
		return rootErr("the root is not a directory")
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return rootErr("the root cannot be inspected")
	}
	real, err := resolve(c.Root)
	if err != nil {
		return rootErr("the root's parent does not exist or cannot be resolved")
	}
	for _, p := range append(append([]string(nil), BuiltinForbidden...), c.ForbiddenPaths...) {
		rp, err := resolve(p)
		if err != nil {
			rp = filepath.Clean(p)
		}
		if overlaps(real, rp) {
			return rootErr("the root overlaps a protected path")
		}
	}
	for dir := filepath.Dir(real); ; dir = filepath.Dir(dir) {
		for _, n := range productionArtifacts {
			if _, err := os.Lstat(filepath.Join(dir, n)); err == nil {
				return rootErr("the root is inside a directory that holds an archive, a wiki, or a config")
			}
		}
		if dir == "/" || dir == "." {
			break
		}
	}
	if !exists {
		return nil
	}
	entries, err := os.ReadDir(c.Root)
	if err != nil {
		return rootErr("the root cannot be read")
	}
	if len(entries) == 0 {
		return nil
	}
	if _, err := readMarker(c.Root); err != nil {
		return rootErr("the root is not empty and is not marked as a trial root")
	}
	return nil
}

// InitRoot runs CheckRoot, creates the root, and writes the marker when the root
// is new or empty. It returns the marker. An existing marker is validated, never
// rewritten.
func (c Config) InitRoot(now time.Time, newID func() string) (Marker, error) {
	if err := c.CheckRoot(); err != nil {
		return Marker{}, err
	}
	if m, err := readMarker(c.Root); err == nil {
		return m, nil
	}
	if err := os.MkdirAll(c.Root, 0o750); err != nil {
		return Marker{}, rootErr("the root cannot be created")
	}
	entries, err := os.ReadDir(c.Root)
	if err != nil || len(entries) != 0 {
		return Marker{}, rootErr("the root is not empty and is not marked as a trial root")
	}
	m := Marker{Mode: MarkerMode, ID: newID(), CreatedAt: now.UTC().Format(time.RFC3339), AcceptedBy: c.AcceptedBy}
	b, _ := json.MarshalIndent(m, "", "  ")
	f, err := os.OpenFile(filepath.Join(c.Root, MarkerName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Marker{}, rootErr("the marker cannot be written")
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return Marker{}, rootErr("the marker cannot be written")
	}
	return m, nil
}

func readMarker(root string) (Marker, error) {
	b, err := os.ReadFile(filepath.Join(root, MarkerName))
	if err != nil {
		return Marker{}, err
	}
	var m Marker
	if err := json.Unmarshal(b, &m); err != nil || m.Mode != MarkerMode || !strings.HasPrefix(m.ID, "trial_") {
		return Marker{}, errors.New("not a trial marker")
	}
	return m, nil
}

// resolve returns the real path of p, following symbolic links. A path that does
// not exist yet is resolved through its nearest existing parent.
func resolve(p string) (string, error) {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r, nil
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(p))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(p)), nil
}

// overlaps reports whether a and b are the same path or one contains the other.
func overlaps(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}

// Paused reports whether the pause file exists in the root.
func (c Config) Paused() bool {
	_, err := os.Lstat(filepath.Join(c.Root, PauseName))
	return err == nil
}

// SetPaused creates or removes the pause file. It touches only that one file.
func (c Config) SetPaused(on bool) error {
	p := filepath.Join(c.Root, PauseName)
	if on {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE, 0o600)
		if err != nil {
			return err
		}
		return f.Close()
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
