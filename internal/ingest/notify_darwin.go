//go:build darwin

package ingest

import (
	"context"
	"os/exec"
	"time"
)

var notificationTimeout = 5 * time.Second

const notificationScript = `on run argv
	display notification (item 2 of argv) with title (item 1 of argv)
end run`

func osascriptNotify(title, body string) error {
	ctx, cancel := context.WithTimeout(context.Background(), notificationTimeout)
	defer cancel()
	return osascriptNotifyContext(ctx, title, body)
}

func osascriptNotifyContext(ctx context.Context, title, body string) error {
	return exec.CommandContext(ctx, "osascript", "-e", notificationScript, title, body).Run()
}

func init() {
	defaultNotify = osascriptNotify
}
