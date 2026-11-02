# Review: research evidence MCP and lint plan

> Status: review of `docs/plans/research-evidence-mcp-lint.md` (non-canonical).
> Code references were checked against `main` on 2026-11-01. Each round's line
> numbers refer to the plan revision it reviewed.

## Implementation (2026-11-02)

The plan is implemented, uncommitted. Canon owns the contract now: `SPEC.md`,
`DESIGN.md`, and `docs/decisions/0026-research-evidence-record.md`. Open minor
items were settled as follows:

- `severity`: `error` for EVID001–EVID003, `warning` for EVID004, EVID005,
  and EVID007.
- Claim `id` must be a non-empty string; a number is EVID002.
- A check or gap with an invalid `kind` gets EVID003 only, not EVID005/EVID007.
- An absent claim `sources` key is EVID004.
- `wiki_write` appends evidence warnings after the existing frontmatter and
  wikilink warnings, in diagnostic sort order.
- Claim IDs sort bytewise (`claim-10` before `claim-2`); accepted as stable.
- "Never auto-promoted" (plan line 109) holds because no code path writes
  `checks`; there is no dedicated test.
- A ref must name a regular file. `storage.Storage` gained `IsFile`, because
  `Exists` is also true for directories; a directory `sources/paper.md/` is
  EVID004.

## Round 4 verdict

Round 4 resolves the unknown-field rule and the `checks`/`gaps` diagnostic
gaps. Its one remaining decision, the search status of invalid records, was
settled in a follow-up edit (below). No blocking item remains; only minor
items are open.

### Round 3 findings resolved

| Round 3 finding | Round 4 plan |
| --- | --- |
| Unknown-field rule contradicted itself | Option (b): unknown keys are ignored and preserved; no fuzzy typo detection; a required-field typo surfaces as that field's missing diagnostic; optional-field typos go undetected (lines 59, 103) |
| No ID for non-array `checks`/`gaps` or missing check/gap `kind` | `EVID003` covers non-array lists, non-object items, and missing, empty, non-string, or out-of-enum `kind`; descendants of an unreadable item are not re-diagnosed; absent `checks`/`gaps` pass (lines 75, 81, 104) |
| Exact-path check | Case and normalization follow the filesystem lookup (lines 59, 102) |

### Search status of invalid records (resolved after round 4)

Line 89 and the acceptance test on line 107 now define it:

- read failure, YAML parse failure, and `EVID001` (missing or non-1
  `version`) are `unavailable` with a reason naming the cause;
- a v1 record with any `EVID002` or `EVID003` is `unavailable` as a whole,
  with a structural-error reason; no partial claim summary;
- a v1 record with only `EVID004`, `EVID005`, or `EVID007` is `recorded`,
  because its structure is readable and search does not resolve `ref`.

### Minor (round 4)

- **`severity` values (unchanged).** Exposed in `lint --json` (line 83) but the
  value set is undefined.
- **Missing claim `sources`.** `EVID004` (line 76) names empty and non-array
  `sources`; say explicitly that an absent key is also `EVID004`.
- **Still open from round 2.** Lexical claim-ID sort (`claim-10` before
  `claim-2`), unspecified `wiki_write` warning order within a page, the
  untestable "never auto-promoted" criterion (line 109), and line 18's
  `status --json` analogy (`status` has no `issues` envelope).

## Round 3 verdict

Round 3 resolves the missing-`version` conflict, the `ref` resolution rule,
the CLI message format, and the installed-schema budget. Two round 2 items are
unchanged; settle them before implementation. The exact-path `ref` rule adds
one implementation caution, listed first under Minor.

### Round 2 findings resolved

| Round 2 finding | Round 3 plan |
| --- | --- |
| Missing `version` handled two ways | A declared block is always validated; no or non-1 `version` is `EVID001` (line 22) |
| `ref` resolution ambiguity | Exact wiki-root-relative `.md` path; no basename, prefix, or case fallback; body wikilinks unchanged (lines 59, 95) |
| CLI message format | Text output omits the ID; new `lint --json` carries `id`, `claim_id`, `severity`, `fix_hint`; old issues get an empty `id` (lines 81, 83) |
| Installed schema rune budget | Server-owned guidance is appended to the `schemaInstructions` result; `_schema.md` is not edited (lines 13–14, 94) |

### Must fix (round 3)

1. **Unknown-field rule is unchanged (line 59).** It still ignores unknown keys
   while forbidding silent acceptance of misspelled standard keys. Choose
   between diagnosing keys outside the v1 key set and documenting optional-key
   typos as undetected.
2. **Diagnostic ID gaps are unchanged.** No ID covers `checks` or `gaps` that
   are not arrays, or a check/gap without `kind`. The search status of a
   structurally invalid record (`EVID001`, `EVID002`) is still undefined; line
   89 maps only parse failures to `unavailable`.

### Minor (round 3)

- **Exact-path check implementation (resolved).** The plan now leaves case
  and Unicode normalization to the wiki filesystem's path lookup (line 59), and
  the acceptance test no longer pins case-variant `ref` results (line 102). A
  plain `storage.Exists` check satisfies it. Case-variant refs may resolve on
  case-insensitive filesystems such as default APFS.
- **`severity` values.** Now exposed in `lint --json` (line 83), so the value
  set is part of a public output contract and must be defined.
- **`lint --json` scope.** A new public CLI mode; SPEC §9.12 needs the shape,
  and line 17's reference to `status --json` should note that `status` emits
  its own struct, not an `issues` envelope.
- **Still open from round 2.** Lexical claim-ID sort (`claim-10` before
  `claim-2`), unspecified `wiki_write` warning order, and the untestable
  "never auto-promoted" criterion (line 107).

## Round 2 verdict

The revised plan resolves every open decision from round 1. Three items are
internally inconsistent or leave a case undecided; settle them before
implementation. The remaining items are minor.

## Round 1 findings resolved

| Round 1 finding | Revised plan |
| --- | --- |
| No closed gap enum | Five gap kinds with meanings; new values need a version bump (line 23) |
| No check/gap conflict table | `EVID006` reserved and never raised in v1 (lines 62, 77), consistent with the no-word-match rule |
| No `wiki_search` summary schema | `{status, claim_count, claims, truncated, reason}` with caps of 3 claims, 4 kinds per list, 200-rune limitation; no `ref` resolution, so no wiki scan per search (line 86) |
| Field gaps | `ref` need not be a subset of page `sources`; URL `ref` is `EVID004`; `replication` `locator` holds the artifact (lines 58, 60) |
| Three frontmatter parsers | One shared parser over raw page bytes (line 66) |
| Nondeterministic lint order | All issues sorted; recorded in SPEC §9.12 (line 68) |
| Stray `recorded` term | Redefined as a search status that is not a certification (line 86) |
| Undefined installed-schema update feature | Narrowed to a documented operating procedure, no migration feature (lines 13, 91) |

## Must fix

1. **Unknown-field rule contradicts itself (line 58).** Unknown keys are
   ignored, yet misspelled standard keys must not be accepted silently. A
   misspelled key is an unknown key. A typo in a required field (`resutl`)
   still surfaces as `EVID005` because the required value is missing, but a
   typo in an optional field (`limitaton`) disappears without a diagnostic.
   "State it in docs and tests" does not define behavior. Choose one:
   - (a) diagnose keys outside the v1 standard key set (new ID or `EVID003`);
   - (b) accept that optional-field typos are not diagnosed and say so.
2. **Missing `version` is handled two ways.** Line 21 validates only when a
   version is present. `EVID001` (line 72) fires when `research_evidence` is
   declared without a version. Pick one; line 72 is safer because it catches
   omissions.
3. **Diagnostic ID gaps.**
   - No ID covers `checks` or `gaps` that are not arrays, or a check/gap with
     no `kind`. `EVID002` covers claims only, and `EVID003` says "not a
     string", which may or may not include a missing value. Line 80 skips
     follow-on diagnostics in these cases but does not say what is reported.
   - The search summary status for a structurally invalid record is
     undefined. Line 86 maps only parse failures to `unavailable`; a page with
     `EVID001` or `EVID002` could be `recorded` or `unavailable`.

## Minor

- **`ref` resolution.** Line 58 defines `ref` as a wiki-root-relative path,
  but the reused resolver (`wikilink.Set.Resolve`) also tries a `sources/`
  prefix and a case-insensitive basename match, so `ref: 자료-a` would pass.
  Decide between exact paths and link resolution.
- **CLI message format.** For CLI lint to report the same ID, the `evidence`
  line message must also carry `[EVID005] claim-1: …`. Only the `wiki_write`
  warning format is specified.
- **Ordering.** lint sorts claim IDs lexically (`claim-10` before `claim-2`);
  the search summary uses document order. The order of `wiki_write` warnings
  within one page is unspecified.
- **Installed schema budget.** Line 91 covers the bundled schema's rune
  budget only. MCP instructions send the installed copy (currently 1,817
  runes), so the operating procedure must also keep the key rules inside its
  first 2,000 runes.
- **Testability of line 104.** The validator never creates records, so "never
  auto-promoted" has no direct test. Restate it as something checkable, such
  as "no code path writes `research_evidence`".
- **`severity` values.** The value set is undefined.

## Round 1 baseline (still valid)

The plan is implementable. Its claims about current contracts match the code:

| Plan claim | Evidence |
| --- | --- |
| MCP instructions send the installed `_schema.md`, cut at 2,000 runes, with a full-read link | `internal/mcp/server.go` `schemaInstructions`, `maxSchemaLen = 2000` |
| `wiki_search` reads each result file and isolates per-result failures | `internal/mcp/tools.go` `handleWikiSearch`; `terms.UnavailableSummary()` is the precedent |
| `wiki_write` returns `{status:"written", path, bytes, warnings: string[]}` and never blocks | `internal/mcp/tools.go` `handleWikiWrite`, SPEC §8.3 |
| lint kinds, `--strict` changes only the exit code, `_schema.md` frontmatter exemption | `cmd/cogvault/lint.go`, SPEC §9.12 |
| Write auto-commit contract unchanged | Commits only when `git.auto_commit` is `write` or `write+ingest`; default `off` |

Constraint: the bundled default schema is 1,979 of 2,000 runes. New guidance
fits only if existing wording is shortened; keep
`TestDefaultContentFitsMaxSchemaLen` passing, because an over-limit fallback
truncates without a link while claiming the full text is included.
