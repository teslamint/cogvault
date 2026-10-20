# Decision verification policy ownership

Date: 2026-09-05
Updated: 2026-09-08
Status: ownership direction agreed; EntireContext guidance delivered

## Question

Should EntireContext require a companion plan or explicit verification scope for decisions that authorize nontrivial runtime changes?

The [originating retrospective](../retros/2026-08-25-wiki-git-safety-net-retro.md) reported unplanned fixes after decision 0024 authorized implementation without declared success criteria.

## Evidence inspected

- EntireContext `README.md` defines the product as Git-anchored decision memory for coding agents.
- EntireContext `AGENTS.md`, Commit Guidelines, connects Spec, ADR, Plan, and Code.
- Its Measure-First Principle requires measurable criteria before behavior changes, with measurement infrastructure checked before implementation.
- Its Planning Contract Enforcement section validates new behavior-changing plans against specifications using `scripts/validate_plan.py`.
- Its ADR bridge keeps lightweight decisions as EC records. Durable project policies receive companion ADRs.
- `src/entirecontext/core/decisions.py`, `create_decision`, accepts rationale, scope, alternatives, and evidence. It does not require a companion plan.

These observations come from the local EntireContext checkout. They distinguish repository development conventions from its decision-memory product contract.
EntireContext searches for `companion plan` and `verification` returned no matching records in this investigation.
Those search results do not establish that no relevant decision exists.

## Preliminary assessment

The existing development rules already cover planned behavior changes inside EntireContext.
They do not establish a universal rule for all projects that store decisions in EntireContext.

A blanket plan requirement would also affect lightweight choices and retrospective rationale records that authorize no implementation.
That would conflict with the current distinction between lightweight EC records and durable policy ADRs.

## Agreed direction

On 2026-09-08, the maintainer selected project-owned policy with optional EntireContext guidance.
This refines the earlier proposal to define one shared rule and enforce it through compound-loop.

- EntireContext recommends that consuming projects define a decision policy and provides an optional template.
- Each consuming project owns its policy content, adoption, and enforcement level.
- A companion plan or verification scope is a project choice, not a universal prerequisite for storing decisions.
- Workflow tools apply the policy that the project adopts. This direction adds no universal compound-loop gate.

## Delivered template scope

The template prompts projects to define which decisions need records, where policy lives, and when decisions need plans or verification evidence.
It also covers approval, exceptions, and updates when a decision becomes obsolete.
These are optional template topics, not mandatory decision fields.

## Follow-up status

EntireContext delivered the guidance and template in [PR #251](https://github.com/teslamint/entirecontext/pull/251) on 2026-09-08.
[Issue #250](https://github.com/teslamint/entirecontext/issues/250) is closed as completed.
Cogvault has not adopted a new companion-plan requirement. Its existing development rules still apply.
This record resolves the ownership direction, not the original process carry-forward's implementation.
