# Review Checklists

Use the smallest checklist that matches the task.

## Plan Review Checklist

Check these first:
- Is the target behavior fully aligned with `SPEC.md`?
- Is layer ownership explicit?
- Are trust boundaries clear?
- Are path, symlink, and exclude semantics assigned to the right layer?
- Are output shapes and identifiers normalized?
- Are deferred items clearly deferred, rather than silently omitted?
- Does the plan describe how the result will be verified?

Common failure modes:
- Caller responsibility is assumed but not enforced
- Contracts are described loosely in prose, not as testable behavior
- A field is introduced without stable semantics
- A fallback path changes data meaning
- Tests are too narrow or only cover the happy path

## Implementation Review Checklist

Read these together:
- target code
- target tests
- related fixtures
- relevant canon
- implementation plan, if one exists

Check these first:
- Does the code implement the planned contract exactly?
- If a plan exists, do plan, canon, and code all still agree?
- Do tests lock the intended behavior and edge cases?
- Do tests hit the contract-owning path, not just helper behavior?
- Are exclusions applied to both directories and files when required?
- Are security checks applied before file access?
- Are normalized values emitted consistently?
- Do fallback parsers preserve intended semantics?
- Does helper extraction remove duplication without changing behavior?

Common failure modes:
- Tests pass but miss contract-violating edge cases
- Plan, code, and canon each describe slightly different behavior
- A regression test proves the database or helper primitive, but not the public invariant
- Security checks exist in one adapter but not another
- External links or attachments pollute internal link fields
- File-system filtering differs between scanner and parser paths
- Refactors centralize logic but change data ordering or dedup behavior

## Role Lens

Append these lenses after findings:

- `junior`
  - Is the implementation understandable and hard to misuse?

- `senior`
  - Are contracts, invariants, and tests coherent?

- `staff`
  - Does this reduce or increase future drift across layers?

- `security engineer`
  - Are trust boundaries and policy enforcement explicit and correctly placed?
