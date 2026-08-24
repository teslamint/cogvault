//go:build darwin

package ingest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const wantNotificationScript = `on run argv
	display notification (item 2 of argv) with title (item 1 of argv)
end run`

func init() {
	argsPath := os.Getenv("COGVAULT_OSASCRIPT_ARGS_PATH")
	if argsPath == "" {
		return
	}

	f, err := os.Create(argsPath)
	if err != nil {
		os.Exit(2)
	}
	err = json.NewEncoder(f).Encode(os.Args[1:])
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestOsascriptNotifyPassesContentOnlyAsArguments(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "quote", value: `before"after`},
		{name: "backslash", value: `before\after`},
		{name: "BEL", value: "before\aafter"},
		{name: "newline", value: "before\nafter"},
		{name: "Unicode separators", value: "before\u2028middle\u2029after"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(executable, filepath.Join(tmpDir, "osascript")); err != nil {
				t.Fatal(err)
			}

			argsPath := filepath.Join(tmpDir, "args.json")
			t.Setenv("PATH", tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("COGVAULT_OSASCRIPT_ARGS_PATH", argsPath)

			title := "title-" + tt.value
			body := "body-" + tt.value
			if err := osascriptNotify(title, body); err != nil {
				t.Fatalf("osascriptNotify() error = %v", err)
			}

			data, err := os.ReadFile(argsPath)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			want := []string{"-e", wantNotificationScript, title, body}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("osascript argv = %#v, want %#v", got, want)
			}
		})
	}
}
