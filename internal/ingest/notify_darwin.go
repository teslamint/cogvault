//go:build darwin

package ingest

import (
	"os/exec"
)

const notificationScript = `on run argv
	display notification (item 2 of argv) with title (item 1 of argv)
end run`

func osascriptNotify(title, body string) error {
	return exec.Command("osascript", "-e", notificationScript, title, body).Run()
}

func init() {
	defaultNotify = osascriptNotify
}
