package main

import (
	"context"
	"testing"
	"time"

	"github.com/teslamint/cogvault/internal/config"
	"github.com/teslamint/cogvault/internal/httpauth"
)

// noopValidator satisfies httpauth.TokenValidator without needing a real
// JWKS/JWT setup. Only its non-nilness matters here — this file tests mode
// acceptance, not credential validation.
type noopValidator struct{}

func (noopValidator) Validate(context.Context, string) (time.Time, error) {
	return time.Time{}, nil
}

// httpauthAcceptsMode reports whether httpauth.Middleware accepts mode
// without panicking. BearerToken and Validator are always populated so a
// rejection can only be attributed to mode itself, never to bearer/oauth's
// own sub-requirements (an empty BearerToken, a nil Validator) that
// validateConfig also checks.
func httpauthAcceptsMode(mode string) (accepted bool) {
	defer func() {
		if recover() != nil {
			accepted = false
		}
	}()
	httpauth.Middleware(httpauth.Config{
		Mode:        mode,
		BearerToken: "placeholder-token",
		Validator:   noopValidator{},
	})
	return true
}

// TestAuthModeAcceptanceMatchesHttpauth proves internal/config's auth.mode
// validation and internal/httpauth's own, independent validateConfig check
// agree on which mode strings are valid.
//
// The two checks are deliberately NOT unified behind a shared constant:
// internal/httpauth is meant to stay standalone — it takes an http.Handler
// and returns one, holds its own Config struct, and is testable with only
// net/http/httptest — so making it import internal/config's YAML schema for
// three string literals would trade a real architectural property (package
// independence) for a small one (literal deduplication). The reverse
// direction (config importing httpauth) is worse still, since config is the
// lower-level package. This test is the drift guard instead: it lives here,
// in cmd/cogvault, because this is the one layer that already imports both
// packages without either importing the other.
//
// What it guards, precisely: if one of the three known modes is removed or
// renamed on one side and not the other, this test fails. It does **not**
// catch an added mode — the cases below are a fixed list, so a fourth mode
// added to one package's switch and forgotten in the other is a string this
// test never submits. TestEveryConfigAuthModeIsAcceptedByHttpauth below covers
// that direction by iterating config.ValidAuthModes() instead of a fixed list,
// and httpauth.validateConfig's startup panic is the fail-closed backstop
// behind both. Saying exactly what each test does and does not cover, because
// an overclaiming comment on a drift guard is worse than no guard: it invites
// the next person to trust a check that was never made.
//
// The empty string is deliberately not a case here: internal/config's
// applyDefaults maps an absent/empty auth.mode to "none" before validate()
// ever runs, so config.Load("") is expected to succeed — that is a
// defaulting behavior, not a claim that "" is a valid mode string, and
// httpauth.Config{Mode: ""} correctly has no such defaulting. Asserting
// those two "accept" the same input for different reasons would not be
// testing agreement; it would be testing an intentional asymmetry.
func TestAuthModeAcceptanceMatchesHttpauth(t *testing.T) {
	cases := []struct {
		mode   string
		accept bool
	}{
		{"none", true},
		{"bearer", true},
		{"oauth", true},
		{"Bearer", false}, // case-sensitive: a plausible typo, not accepted
		{"NONE", false},
		{"foo", false},
	}

	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			authYAML := "auth:\n  mode: " + tc.mode + "\n"
			if tc.mode == "oauth" {
				authYAML += "  oauth:\n    issuer: https://issuer.example.com\n"
			}
			configPath, _, _ := testVaultWithAuth(t, authYAML)
			_, err := config.Load(configPath)
			configAccepts := err == nil

			httpauthAccepts := httpauthAcceptsMode(tc.mode)

			if configAccepts != tc.accept {
				t.Fatalf("config.Load acceptance for mode %q = %v, want %v", tc.mode, configAccepts, tc.accept)
			}
			if httpauthAccepts != tc.accept {
				t.Fatalf("httpauth.Middleware acceptance for mode %q = %v, want %v", tc.mode, httpauthAccepts, tc.accept)
			}
			if configAccepts != httpauthAccepts {
				t.Fatalf("internal/config and internal/httpauth disagree on mode %q: config accepts=%v, httpauth accepts=%v", tc.mode, configAccepts, httpauthAccepts)
			}
		})
	}
}

// TestEveryConfigAuthModeIsAcceptedByHttpauth closes the drift direction the
// fixed case list above cannot reach: a mode added to internal/config's
// allow-list and forgotten in internal/httpauth's switch. That is the
// dangerous direction — config would load such a config happily and
// httpauth.Mount would then panic at startup — and it is dangerous precisely
// because a fixed list of known modes never submits the new string.
//
// Iterating config.ValidAuthModes() means adding a mode there without
// teaching httpauth about it fails this test instead of the operator's
// startup. The reverse direction, httpauth accepting something config
// rejects, is unreachable in production because config gates first.
func TestEveryConfigAuthModeIsAcceptedByHttpauth(t *testing.T) {
	modes := config.ValidAuthModes()
	if len(modes) == 0 {
		t.Fatal("config.ValidAuthModes() is empty; the drift guard would vacuously pass")
	}
	for _, mode := range modes {
		t.Run(mode, func(t *testing.T) {
			if !httpauthAcceptsMode(mode) {
				t.Fatalf("internal/config accepts auth.mode %q but internal/httpauth panics on it; "+
					"a config that loads cleanly would crash at startup in httpauth.Mount", mode)
			}
		})
	}
}

// TestMaxStreamSecondsCeilingRejected pins the upper bound on
// auth.max_stream_seconds. Without it, a large value overflows the int64
// nanosecond durations derived from it — the session sweeper's idle TTL is
// twice this value — into a negative duration, which mcp-go reads as "sweeper
// disabled" and which silently restores the session leak the sweeper exists to
// prevent. Failing open to a leak on an absurd number is worse than refusing
// the number.
func TestMaxStreamSecondsCeilingRejected(t *testing.T) {
	configPath, _, _ := testVaultWithAuth(t, "auth:\n  mode: none\n  max_stream_seconds: 5000000000\n")
	if _, err := config.Load(configPath); err == nil {
		t.Fatal("config.Load accepted an auth.max_stream_seconds that overflows the derived durations, want an error")
	}
}
