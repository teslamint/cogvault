//go:build darwin

package ingest

import (
	"fmt"
	"os/exec"
)

func osascriptNotify(title, body string) error {
	return exec.Command("osascript", "-e",
		fmt.Sprintf(`display notification %q with title %q`, body, title)).Run()
}

func init() {
	defaultNotify = osascriptNotify
}
