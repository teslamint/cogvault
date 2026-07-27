---
title: "F9: Configurable max file size for ingest"
status: draft
created: 2026-07-27
---

# F9: Configurable max file size for ingest

## Problem

The 32MB `maxFileSize` cap in `internal/ingest/ingest.go` is hardcoded. The real
corpus contains a 42MB PDF (NYT Satoshi article) that is permanently skipped.
Users cannot raise the cap without recompiling.

Viability confirmed: `claude --print` successfully digests a 42MB PDF (29s, ~214K
input tokens, $2.95).

## Design

Add a `max_file_size_mb` field to `config.yaml` (top-level, matching the existing
bare-int convention of `consistency_interval`). The ingest runner reads it from
config instead of the hardcoded constant.

### Config shape

```yaml
max_file_size_mb: 64   # default: 32; unit: megabytes
```

Top-level placement (not a nested `ingest:` block) because one field does not
justify a new nesting level.

### Behavior

- Zero or omitted: default 32MB (preserves current behavior).
- Negative: validation error at config load (`max_file_size_mb: must be positive`).
- Positive: value × 1MB becomes the ingest runner's `maxFileSize`.

### Affected files

| File | Change |
|------|--------|
| `internal/config/config.go` | Add `MaxFileSizeMB int` field + yaml tag; zero-guard in `applyDefaults`; negative-value validation |
| `internal/ingest/ingest.go` | `New` wires `cfg.MaxFileSizeMB * 1<<20` → `Runner.maxFileSize`; package const `maxFileSize` remains as the default value source |
| `internal/config/config_test.go` | Validation test for negative value |
| `internal/ingest/ingest_test.go` | Existing `h.runner.maxFileSize = 8` seam unchanged; add one test confirming config wiring |
| `DESIGN.md` §2.2, §2.7 | Update Config struct listing and "32MB size cap" text |
| `SPEC.md` §10 constants (line 539) and §flow (line 470) | Update both "32MB" references to note configurability |

### Forward/backward compatibility

`KnownFields(true)` means an old binary rejects `max_file_size_mb` in config.
This is the accepted tradeoff per decision `1dd6ef9a-c4f` (typo detection over
forward compatibility). Omitting the field on old+new binaries preserves the 32MB
default.

### Cost note

Raising the cap is a spend knob: the viability test showed $2.95 for a 42MB PDF
(minimal prompt, 681 output tokens) versus $0.97–$1.39 for a 53KB PDF (O1 spike).
A full digest prompt will cost more. There is no per-file cost guard in ingest.

## Success criteria

- SC1a (unit-testable): `New` produces a Runner whose `maxFileSize` equals
  `cfg.MaxFileSizeMB<<20`; a synthetic file above the configured cap skips, below
  digests.
- SC1b (manual, ~$3): with `max_file_size_mb: 64`, the NYT PDF digests instead of
  appearing in `skipped`. Single real-corpus run, not a suite assertion.
- SC2: A config with the field omitted or set to 0 behaves identically to today
  (32MB default).
- SC3: A config with a negative value is rejected at load time with a clear error.
- SC4: `DESIGN.md` and `SPEC.md` reflect the new configurable cap.

## Non-edits

`docs/decisions/0021-v2-refounding.md` states "max file size 32MB" but is an
accepted decision record describing the original design. It is not edited — the
config default preserves the original decision; the new field only lets users
override it.

## Open decisions

None — the design is minimal and unambiguous.
