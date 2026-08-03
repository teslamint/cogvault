# Deviation: orphan sweep review remediation

Date: 2026-08-03
Area: orphan sweep safety, archive exclusion, and storage permissions

## Original contract

The approved design is `docs/specs/2026-08-03-orphan-sweep-archive-design.md`.
The approved plan is `docs/plans/2026-08-03-001-feat-orphan-sweep-archive-plan.md`.

The approved artifacts define three relevant behaviors:

1. The sweep uses one `os.Stat` call to decide if a source directory is available.
2. The default `exclude` list contains `sources/_archived` only when users omit `exclude`.
3. `Storage.Move` resolves both paths and calls `os.Rename` under the storage mutex.

The canonical contract also states two stronger requirements:

- `SPEC.md` says that `sources/_archived` is excluded from indexing.
- `SPEC.md` denies writes to every `exclude_read` path.

## Discovered contradictions

Review round 1 found three P1 contradictions.

First, existing generated configs contain an explicit old default exclude list.
Those configs do not receive `sources/_archived` after an upgrade.
The consistency scan can therefore reindex archived pages.

Second, one successful directory `Stat` does not prove continued source availability.
An empty mountpoint can remain after an external volume disappears.
The source can also disappear after the guard succeeds.
Both cases can archive every page for that source.

Third, `Storage.Move` does not enforce `exclude_read` for either path.
The method can move a protected page or write into a protected subtree.

The same review found five P2 gaps:

- cancellation does not stop the sweep;
- a restored source can still lose its live page;
- a move followed by a ledger failure can strand a success row;
- the dry-run test does not prove ledger immutability; and
- `os.Rename` can replace an existing archive destination.

## Necessity

Documentation changes alone cannot resolve these findings.
Existing configs would still index archived pages.
An unavailable source could still trigger a mass archive.
The new storage method would still violate the permission contract.

The remediation must change observable behavior.
The approved spec and plan must remain unchanged as historical records.

## Observable behavior

The remediation defines these behaviors:

1. `sources/_archived` is an internal scan exclusion.
   `Config.AllExcluded` includes it for every config.
   An explicit user exclude list cannot remove this internal exclusion.
2. A source directory needs survivor proof before the sweep archives any row.
   A snapshot must contain at least one exact source path from its success rows.
   The snapshot must contain exactly one missing success-row source.
   The sweep skips zero-survivor and multi-missing states as ambiguous.
3. The sweep checks the exact directory entry again before each page move.
   A restored source cancels that archive action.
4. The sweep accepts a context and checks cancellation before each candidate.
   A canceled run stops before another archive mutation.
5. `Storage.Move` rejects an `exclude_read` source or destination.
   It returns `ErrPermission` through the existing error mapping.
6. `Storage.Move` refuses to replace an existing destination.
   It returns an error that wraps `os.ErrExist`.
7. A success ledger row is unchanged only while its wiki page exists.
   If the page is missing, ingest rebuilds the page from the present source.
8. Dry-run reports archive candidates without changing storage or the ledger.
9. Archived pages remain readable through an exact wiki path.
   The archive retains them until the user removes them manually.
   The internal exclusion affects scans and indexes, not direct reads.

The survivor proof is conservative.
If every tracked source disappears, the sweep treats the state as ambiguous.
If several tracked sources disappear, the sweep also treats the state as ambiguous.
The pages remain live until a later run observes one missing tracked source.

## Safety and consent boundaries

The remediation does not write to `sources[]`.
It does not add an outward publication step.
It does not add a new user approval gate.

The existing ingest lock still serializes ingest runs.
The storage mutex still serializes wiki mutations.
Dry-run remains non-mutating.
Cancellation stops future sweep mutations but does not roll back completed rows.

## Verification changes

The remediation adds these tests:

- an old explicit exclude list still excludes `sources/_archived`;
- an empty source directory cannot trigger a mass archive;
- a partially available directory cannot archive several missing sources;
- a restored exact source path cancels an archive action;
- a canceled context causes no archive mutation;
- a missing live page for a success row is rebuilt;
- a move-then-ledger-failure state heals on the next run;
- `Storage.Move` rejects both `exclude_read` directions;
- `Storage.Move` preserves both files when the destination exists;
- dry-run preserves the success row and creates no archive destination;
- archive names preserve distinct hash suffixes;
- source directory and move errors preserve pages and ledger rows; and
- an archived page remains directly readable through its exact path.

Run `go test -race ./...` and `go vet ./...` after the targeted tests pass.

## Traceability

- Approved spec: `docs/specs/2026-08-03-orphan-sweep-archive-design.md`.
- Approved plan: `docs/plans/2026-08-03-001-feat-orphan-sweep-archive-plan.md`.
- Review record: `.release-loop/progress.md`, review round 1 at `2026-08-03T03:48:33Z`.
- Affected code: `internal/config`, `internal/storage`, and `internal/ingest`.
- Canonical docs: `SPEC.md` and `DESIGN.md`.
- Acceptance evidence: targeted tests, the full race suite, vet, and review round 2.
