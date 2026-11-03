package archive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPickupMapsCapturesAndLookupNeedsMatchingBytes(t *testing.T) {
	a, root, dest := handoffFixture(t)
	first := mustCapture(t, a, verified("mapped bytes\n", "m.md", "https://example.org/1"))
	if _, err := a.Handoff(ctx, DefaultHandoffTypes); err != nil {
		t.Fatal(err)
	}
	picked, err := Pickup(root, dest, DefaultHandoffTypes)
	if err != nil {
		t.Fatal(err)
	}
	src := picked[0].SourcePath
	hexSum := hexOf(first.ObjectID)
	obj, ids, err := CapturesForSource(src, hexSum)
	if err != nil || obj != first.ObjectID || len(ids) != 1 || ids[0] != first.CaptureID {
		t.Fatalf("lookup = %s %v %v", obj, ids, err)
	}
	if obj, ids, _ := CapturesForSource(src, strings.Repeat("0", 64)); obj != "" || ids != nil {
		t.Fatal("lookup matched other bytes")
	}
	if obj, _, _ := CapturesForSource(filepath.Join(dest, "other.md"), hexSum); obj != "" {
		t.Fatal("lookup matched another file name")
	}

	second := mustCapture(t, a, verified("mapped bytes\n", "m.md", "https://mirror.example.org/1"))
	if _, err := a.Handoff(ctx, DefaultHandoffTypes); err != nil {
		t.Fatal(err)
	}
	if _, err := Pickup(root, dest, DefaultHandoffTypes); err != nil {
		t.Fatal(err)
	}
	if _, ids, _ := CapturesForSource(src, hexSum); len(ids) != 2 || !oneOf(second.CaptureID, ids) {
		t.Fatalf("second capture not mapped: %v", ids)
	}

	mapPath := filepath.Join(dest, CaptureMapName)
	os.WriteFile(mapPath, []byte(`{"version":1,"entries":{}}{}`), 0o600)
	if _, _, err := CapturesForSource(src, hexSum); err == nil {
		t.Fatal("malformed map accepted")
	}
	os.WriteFile(mapPath, []byte(`{"version":1,"entries":{"`+filepath.Base(src)+`":{"object_id":"`+first.ObjectID+`","capture_ids":["not-a-capture"]}}}`), 0o600)
	if _, _, err := CapturesForSource(src, hexSum); err == nil {
		t.Fatal("invalid capture ID accepted")
	}
	os.WriteFile(mapPath, []byte(`{"version":1,"entries":{"`+filepath.Base(src)+`":{"object_id":"`+first.ObjectID+`","capture_ids":["`+first.CaptureID+`","`+first.CaptureID+`"]}}}`), 0o600)
	if _, _, err := CapturesForSource(src, hexSum); err == nil {
		t.Fatal("repeated capture ID accepted")
	}
	os.Remove(mapPath)
	os.Symlink(filepath.Join(t.TempDir(), "elsewhere.json"), mapPath)
	if _, _, err := CapturesForSource(src, hexSum); err == nil {
		t.Fatal("symlinked map accepted")
	}
}

func TestPageRecordsCapture(t *testing.T) {
	meta := map[string]any{PageObjectIDKey: "sha256:aa", PageCaptureIDsKey: []any{"cap_1", "cap_2"}}
	for _, tc := range []struct {
		capture, object string
		want            bool
	}{
		{"cap_1", "sha256:aa", true}, {"cap_2", "sha256:aa", true}, {"cap_3", "sha256:aa", false}, {"cap_1", "sha256:bb", false},
	} {
		if got := PageRecordsCapture(meta, tc.capture, tc.object); got != tc.want {
			t.Errorf("%s/%s = %v", tc.capture, tc.object, got)
		}
	}
	if PageRecordsCapture(map[string]any{PageCaptureIDsKey: "cap_1", PageObjectIDKey: "sha256:aa"}, "cap_1", "sha256:aa") {
		t.Error("a scalar capture list was accepted")
	}
}
