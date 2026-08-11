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
// test never submits. The backstop for that case is httpauth.validateConfig's
// startup panic, which fails closed on any mode it does not recognize. Stating
// this plainly because an overclaiming comment on a drift guard is worse than
// no guard: it invites the next person to trust a check that was never made.
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
