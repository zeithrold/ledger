---
name: ledger-debug
description: Reproduce and repair Ledger defects using runtime evidence, targeted hypotheses and verified regression tests. Use for failing tests, runtime failures, intermittent bugs and UI regressions; preserve a diagnosis-only request without applying repairs.
---

# Evidence-driven debug

Read `AGENTS.md`, `docs/governance.md` and the affected domain contract. Inspect the dirty diff before changing files. Use the pinned tool's `debug-*` commands for session and evidence management; `just --list` lists the supported recipes and each reports its own argument usage. A session report records evidence, not proof that a human reproduced a device-only issue.

1. Record trigger, actual/expected behavior, platform and success criteria. Start `just debug-start <case>` and fill `expected`, `actual`, `hypotheses` and `failure_pattern` in `build/debug/<session>/session.json`; record probes in `probes`. The pattern is a regular expression identifying the intended failing assertion, not a generic error/exit marker. Use synthetic inputs and an isolated database.
2. Reproduce the smallest relevant failure. Separate observed facts from hypotheses. For ambiguous defects, list competing causes and a falsifying observation for each; do not invent extra hypotheses for an obvious failure.
3. Prefer existing structured logs and test output. If insufficient, add narrowly scoped temporary probes and record their locations. Follow repository logging/redaction rules. Never capture credentials, config objects, entire request bodies or browser storage.
4. Run `just debug-run <session> <profile>` using a named `debug_profiles` entry from `governance.json`. Go uses `go test -json` and request-correlated slog. Flutter uses widget/native integration tests and existing capture drivers. Future web uses Playwright traces. Process streams stay on the terminal; the session stores results and fingerprints, not raw logs. Save only deliberately sanitized additional evidence. Use Delve or DevTools only when a specific hypothesis needs them.
5. Add a regression that fails for the original defect, then make the smallest justified repair. For red/green comparison, use an isolated worktree or temporary copy; never reset the shared checkout. For intermittent failures, record attempt counts and tested schedule/seed.
6. Run `just debug-verify <session> <profile>` after repair. It requires a failed reproduction of the same profile on a different source fingerprint. Confirm the failure was the intended assertion, not missing tooling, and keep fixtures unchanged. Run affected quality gates. Missing devices, SDKs or credentials are blocked; a build or mocked keyboard is not a native interaction result.
7. Remove only this session's temporary instrumentation and rerun affected checks. Persistent diagnostics must meet normal observability contracts. Obtain independent final-diff architecture review.
8. Produce `debug-report` with root cause, linked observations, regression evidence, validation and remaining limits. Keep raw artifacts ignored. Put reusable rules in maintained docs and dated Chinese evidence in the manuscript repository.

If a scenario cannot be reproduced, report attempts and the next discriminating observation. Do not claim a fix from inspection alone or change unrelated code to chase an unobserved hypothesis. Debugging does not authorize production mutation or external evidence sharing.
