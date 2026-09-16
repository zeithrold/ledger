# Ledger engineering rules

## Contracts and ownership

- Read `docs/architecture.md`, `docs/governance.md` and the affected domain guide before changing code. Durable documentation, code, comments and commit messages are English; dated Chinese execution records belong in `../ledger-manuscript/development-records/`.
- Preserve unrelated changes. Stage only the paths owned by the current task. Do not reset, clean, amend or rewrite other work to make a check pass.
- Keep dependency direction explicit: HTTP adapters translate the public contract, services own business decisions, repository implementations own persistence, and pure accounting functions do not depend on HTTP or database infrastructure. Use constructor injection and narrow consumer-owned interfaces.
- Do not add a generic framework, service locator, global mutable registry or abstraction for hypothetical future consumers. Enforce existing boundaries with architecture checks before introducing new layers.
- OpenAPI is the HTTP source of truth. Exact API revision headers, stable RFC 7807 types, tenant isolation, integer minor units and balanced immutable journals are contracts. Change producers, consumers, generated artifacts and tests together.
- sqlc/pgx own SQL access. Schema changes are reviewed migration files; executing migrations against persistent databases requires the user's authorization. Testcontainers use disposable databases and never `.env.local`.

## Mechanical quality gates

- Use `just` recipes, which delegate to the pinned Go bootstrap and `governance.json`. Do not introduce Python, Bash wrappers, machine-specific absolute paths or unpinned tool installers.
- Run `just check` before delivery, then the affected integration, fuzz, mutation or UI checks identified by `just changes`. A missing dependency/device is blocked, never a passed or silently skipped check.
- Coverage targets are 70% overall and 90% changed executable code. Only a measured, reviewed, expiring baseline can temporarily lower the overall floor; new code does not inherit that exception. Do not exclude handwritten business logic to improve a score.
- Fixes require a regression that fails for the original behavior. Prefer observable state and invariants over implementation-mirroring assertions. Keep fixtures deterministic and credential-free.
- Temporary instrumentation obeys `docs/observability.md`: structured scalar logs, redaction, no raw request bodies, configuration dumps or credentials. Use the [debug workflow](.agents/skills/ledger-debug/SKILL.md) for reproduction-driven diagnosis and repair.

## Independent review and delivery

- Every code change requires an independent Subagent architecture review of the final diff using [ledger-architecture-review](.agents/skills/ledger-architecture-review/SKILL.md). The reviewer must not implement the reviewed change. Contract, dependency-boundary and shared-component changes also require design review before implementation.
- Give the reviewer requirements, the base revision and evidence; let it independently inspect callers and constraints. Resolve blocking findings or explicitly record why they are invalid. Do not write a reviewer identity or verdict on the reviewer's behalf.
- Review evidence must match the final source/diff fingerprint. A mechanical report check proves completeness/freshness, not independent identity or architectural correctness.
- Follow `CONTRIBUTING.md` for commits and PRs. Report passed, failed, blocked and unrun checks separately; local execution is not proof of remote CI, deployment or native-device success.
