package errors

import (
	"errors"
	"fmt"
	"testing"
)

func TestSentinelsAreDistinct(t *testing.T) {
	sentinels := []error{ErrNotFound, ErrPermission, ErrTraversal, ErrSymlink, ErrNotMarkdown}
	for i, a := range sentinels {
		for j, b := range sentinels {
			if i != j && errors.Is(a, b) {
				t.Errorf("sentinel %q should not match %q", a, b)
			}
		}
	}
}

func TestWrappingPreservesIdentity(t *testing.T) {
	sentinels := []error{ErrNotFound, ErrPermission, ErrTraversal, ErrSymlink, ErrNotMarkdown}
	for _, s := range sentinels {
		wrapped := fmt.Errorf("storage.Read foo.md: %w", s)
		if !errors.Is(wrapped, s) {
			t.Errorf("wrapped error should match %q", s)
		}
	}
}

func TestErrorStringsNonEmpty(t *testing.T) {
	sentinels := []error{ErrNotFound, ErrPermission, ErrTraversal, ErrSymlink, ErrNotMarkdown}
	for _, s := range sentinels {
		if s.Error() == "" {
			t.Error("sentinel error string should not be empty")
		}
	}
}
