---
module: cmd/cogvault
date: "2026-09-24"
problem_type: best_practice
component: stderr_timestamp_contract
severity: medium
applies_when:
  - "a contract requires an offset-bearing timestamp format (numeric offset, not Z)"
  - "a clock seam is injected into code under test and the fixtures pick a time zone"
  - "a plan or spec asserts Go time layout semantics from recall rather than from a run"
  - "a test's fixture varies one axis (the instant) but the contract is about another (the zone)"
tags:
  - go-time
  - rfc3339
  - test-fixtures
  - contracts
  - clock-seam
---

# RFC 3339 with `Z07:00` prints `Z` on UTC; offset-bearing contracts need `-07:00`

## Context

The ingest run-timestamp contract (`SPEC.md` §9.4) requires each
`cogvault ingest` bracket line to carry "RFC 3339 timestamps in the process's
local time zone with a numeric offset" (spec Scope/In,
`docs/specs/2026-09-24-ingest-run-timestamps-design.md`).

The approved plan's Architecture Decision 3
(`docs/plans/2026-09-24-001-feat-ingest-run-timestamps-plan.md`) implemented
this as `ingestNow().Format(time.RFC3339)` and justified it with:

> `time.RFC3339` prints the value's own zone offset

That claim rests on recalled layout semantics, not a run. Go's `time.RFC3339`
layout is `2006-01-02T15:04:05Z07:00`, and `Z07:00` means *print `Z` for zero
offset* (ISO 8601 UTC designator) — not *print the offset*. So on a UTC host
the produced stamp ends in `Z`, which satisfies "RFC 3339" but violates the
"numeric offset" half of the contract. The `-07:00` form is the layout that
always prints a signed numeric offset.

Every test fixture in the change used
`kstZone = time.FixedZone("KST", 9*60*60)`, and the plan's discrimination
table varied only the time value (seconds 0 and 5), never the zone. With a
non-zero offset both layouts print `+09:00`, so `Z07:00` and `-07:00` were
indistinguishable across the entire suite. Every test passed while the
contract was violated.

The defect was found by final-branch review (fingerprint
`rfc3339-utc-prints-z-not-numeric-offset`) and fixed in `6825be7`:

```go
// ingestRunTimeLayout is the timestamp format for the run bracket lines. It is
// RFC3339 with an explicit numeric offset: time.RFC3339 prints "Z" on a UTC
// host, but the run timestamps contract requires a numeric offset (+00:00).
const ingestRunTimeLayout = "2006-01-02T15:04:05-07:00"
```

with a new fixture whose only changed axis is the zone:

```go
func TestBracketIngestRunUTCPrintsNumericOffset(t *testing.T) {
	fixedIngestClock(t,
		time.Date(2026, 9, 24, 1, 0, 0, 0, time.UTC),
		time.Date(2026, 9, 24, 1, 0, 5, 0, time.UTC),
	)
	// asserts "2026-09-24T01:00:00+00:00 ingest start ..."
}
```

Measured (Go, `time.Date(2026, 9, 24, 1, 0, 0, 0, loc)`):

| layout | `time.UTC` | `FixedZone("KST", 9h)` |
|---|---|---|
| `time.RFC3339` (`Z07:00`) | `2026-09-24T01:00:00Z` | `2026-09-24T01:00:00+09:00` |
| `"2006-01-02T15:04:05-07:00"` | `2026-09-24T01:00:00+00:00` | `2026-09-24T01:00:00+09:00` |

Mutation check: at `6825be7^` with only the new test file taken from `6825be7`,
`TestBracketIngestRunUTCPrintsNumericOffset` fails with
`unexpected start line: "2026-09-24T01:00:00Z ..."`; at `6825be7` it passes.

## Guidance

1. **Never pick a timestamp layout from recalled semantics when the contract
   names the format.** For an offset-bearing contract, write the explicit
   layout (`-07:00` for always-signed offset) or assert the format in a test
   whose fixture forces the discriminating zone. In Go, `Z07:00` (inside
   `time.RFC3339`/`time.RFC3339Nano`) and `-07:00` differ *only* at zero
   offset — a whole class of hosts (UTC CI runners, containers, servers with
   `TZ=UTC`) hits exactly that case, so production and dev machines disagree.
2. **A field that varies across the test suite must vary along the axis the
   contract constrains.** Here the constrained axis was the *zone*, but every
   fixture varied only the *instant*. Two implementations that agree on
   `+09:00` are not thereby proven to agree on `+00:00`.
3. **Clock-seam fixtures should include a zero-offset case by default.** When
   a seam returns `time.Time`, add at least one `time.UTC` fixture alongside
   the local-zone ones. It costs one test and it is the only case that
   exercises the `Z`-vs-offset branch.
4. **A plan's "why this works" line is an assumption, not evidence.** Decision
   3 stated a Go formatting property with no command or output behind it, and
   the spec's assumption table did not carry a row for it. Formatting claims
   belong in the assumption table with an observed run, alongside the
   filesystem and query claims.
5. **Fix at the layout constant, not at the call site.** A single named
   `ingestRunTimeLayout` documents why `time.RFC3339` was not usable and keeps
   both bracket lines from drifting; a `strings.Replace(s, "Z", "+00:00", 1)`
   post-process would be fragile and zone-blind.

## Why This Matters

The failure is silent and environment-dependent. The suite is green on a
developer machine in KST; the contract breaks on any UTC host, including CI,
containers, and the launchd context the feature exists to serve. The operator
reading the launchd stderr log—the whole point of the change—would have seen
`Z`, not the required numeric offset.

The deeper cost is that a plausible-sounding sentence in an approved plan
("`time.RFC3339` prints the value's own zone offset") propagated into code
unchallenged, because no artifact in the chain was positioned to contradict it:
the tests agreed with it by construction. Layout semantics are exactly the
kind of claim that reads as obviously true and is off by one special case.

## When to Apply

Use this guidance when:

- a spec or contract names a timestamp format property (numeric offset, no
  `Z`, fixed precision, local vs UTC) and the implementation formats a
  `time.Time`;
- test fixtures inject a clock/zone and the contract's constrained axis is the
  zone rather than the instant;
- a plan or design doc justifies a formatting choice from language semantics
  without a command and observed output;
- reviewing a change whose tests pass but which touches timezone handling or
  anything else with an environment-dependent branch.

Not about general timezone correctness (DST, monotonic clocks, parsing
untrusted timestamps) — this is narrowly about *formatting layouts* and about
fixture axis coverage.

## Examples

**Incorrect**: `ingestNow().Format(time.RFC3339)` justified by "prints the
value's own zone offset", with every fixture in `FixedZone("KST", 9h)` and a
discrimination table that varies only the timestamp value. Green suite,
contract violated on UTC hosts (`6825be7^`).

**Correct**: `const ingestRunTimeLayout = "2006-01-02T15:04:05-07:00"`, plus a
fixture that changes *only* the zone to `time.UTC` and asserts the `+00:00`
suffix. The test fails before the fix (`unexpected start line:
"2026-09-24T01:00:00Z ingest start origin=interactive"`), passes after
(`6825be7`).

**Related shape**: the same reasoning applies to precision and separators —
e.g. asserting a contract that requires fractional seconds while every fixture
happens to land on a whole second; the fixture must vary the axis the contract
names.
