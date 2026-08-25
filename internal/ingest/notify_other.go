//go:build !darwin

package ingest

func init() {
	defaultNotify = func(_, _ string) error { return nil }
}
