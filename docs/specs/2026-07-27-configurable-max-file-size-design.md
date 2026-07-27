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

- Zero or omitted: `applyDefaults` sets 32 (the sole default owner; the ingest
  package const is deleted). The zero-guard uses `== 0` (not `<= 0`) so negatives
  reach validation — this differs from `consistency_interval` which silently
  replaces `<= 0`.
- Negative: validation error at config load (`max_file_size_mb: must be positive;
  expected a value in megabytes`).
- Positive: `int64(value) << 20` becomes the ingest runner's `maxFileSize`. No
  upper-bound validation — the cost note below serves as the user-facing guard.

### Affected files

| File | Change |
|------|--------|
| `internal/config/config.go` | Add `MaxFileSizeMB int` field + yaml tag; `== 0` guard in `applyDefaults`; `< 0` validation in `validate()` |
| `internal/ingest/ingest.go` | `New` wires `int64(cfg.MaxFileSizeMB) << 20` → `Runner.maxFileSize`; delete package const `maxFileSize` (default now owned by config) |
| `internal/config/config_test.go` | Validation test for negative value |
| `internal/ingest/ingest_test.go` | Existing `h.runner.maxFileSize = 8` seam unchanged; add one test confirming config wiring |
| `DESIGN.md` §2.2 | Update Config struct listing (also add `LLMConfig.Model` which is currently missing from the listing) |
| `DESIGN.md` §2.7 | Update "32MB size cap" to "configurable size cap (default 32MB)" |
| `SPEC.md` §10.7 (line 537–540) | `max file size 32MB` promoted to config key; update the "promoted on demonstrated need" clause to record this as the first such promotion |
| `SPEC.md` §flow (line 470) | Update "size cap (32MB; ...)" to note configurability |
| `docs/research/v2-follow-ups.md` | Update F9 row status |

### Forward/backward compatibility

`KnownFields(true)` means an old binary rejects `max_file_size_mb` in config.
This is the accepted tradeoff per `docs/decisions/0001-config-validation.md`
(typo detection over forward compatibility).

`cogvault init` (`Save`) marshals `DefaultConfig()` without `omitempty`, so a
freshly initialized config will contain `max_file_size_mb: 32`. This means a
config created by a new binary cannot be used with an old binary — the same
forward-compatibility tradeoff as all other fields. Omitting the field in
hand-written configs preserves old-binary compatibility.

### Cost note

Raising the cap is a spend knob: the viability test showed $2.95 for a 42MB PDF
(minimal prompt, 681 output tokens) versus $0.97–$1.39 for a 53KB PDF (O1 spike).
A full digest prompt will cost more. There is no per-file cost guard in ingest.

## Success criteria

- SC1a (unit-testable): `New` produces a Runner whose `maxFileSize` equals
  `int64(cfg.MaxFileSizeMB) << 20`; a synthetic file above the configured cap
  skips, below digests.
- SC1b (manual, ~$3): with `max_file_size_mb: 64`, the NYT PDF digests instead of
  appearing in `skipped`. Single real-corpus run, not a suite assertion.
- SC2: A config with the field omitted or set to 0 behaves identically to today
  (32MB default).
- SC3: A config with a negative value is rejected at load time with a clear error.
- SC4: `DESIGN.md` and `SPEC.md` reflect the new configurable cap.

## Non-edits

- `docs/decisions/0021-v2-refounding.md` states "max file size 32MB" — accepted
  decision record describing the original design. Not edited; the config default
  preserves the original decision.
- `docs/specs/2026-07-22-refound-capture-pipeline-design.md` line 87 states
  "max file size 32MB" in the behavior-knobs-are-constants principle. Not edited;
  it describes the original design stance. This spec is the "demonstrated need"
  that the original text anticipated.

## Open decisions

None.
