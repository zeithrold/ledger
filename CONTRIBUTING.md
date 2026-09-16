# Contributing

Read [engineering rules](AGENTS.md), [governance](docs/governance.md) and the affected domain contract. Use a `codex/` branch unless the task specifies another branch. Keep each commit one reviewable behavior change, with its tests and required contract/documentation updates.

## Commit and pull request format

Use Conventional Commits:

```text
type(scope)!: imperative summary

Explain the concrete problem, behavior after the change, and any migration.

BREAKING CHANGE: required consumer action, if applicable
Refs: issue or task identifier, when one exists
```

Types are `feat`, `fix`, `refactor`, `perf`, `test`, `docs`, `build`, `ci`, `chore` and `revert`. Use a domain scope such as `accounting`, `identity`, `api`, `tooling` or `governance`. Mark incompatible public contracts with `!` and explain the required migration. Never invent an issue identifier. Prefer a subject of at most 100 characters.

The PR title uses the same syntax and is checked in CI. A squash merge must preserve that title and the material validation/migration notes. Local hooks are convenience only; protected-branch required checks are the enforcement boundary and require repository-host configuration.

Before staging, inspect `git status` and the diff. Stage explicit paths; do not include unrelated local edits, secrets, raw debug evidence or build outputs. Inspect the staged diff before committing. Do not rewrite published history without an explicit request.

## Reviewable evidence

The PR body describes the trigger and resulting behavior, validation commands/results, independent architecture findings and their resolution, and material limitations. Link UI before/after evidence for UI changes. For fixes, identify the regression test and red/green evidence. State when Docker, a device, credentials or remote CI were unavailable.

Run `just check` and `just changes`; run the additional affected checks from the change report. Follow the project review skill to produce fresh independent review evidence. Coverage exceptions must include measured numerator/denominator, owner, reason, expiry and a remediation task; they are reviewed changes, not local overrides.
