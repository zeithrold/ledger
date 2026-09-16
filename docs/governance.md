# Architecture and quality governance

This policy applies to the Go service, Flutter app and future web client. Each repository declares its executable commands and language-specific adapters in `governance.json`; `just` is the developer entrypoint and the pinned Go tool executes argument arrays without a shell. A web repository will adopt the same report and review contracts when it is created; no web runtime is introduced into the backend.

## Quality gates

| Gate | Required behavior |
| --- | --- |
| Formatting and lint | No formatting drift, analyzer/compiler errors, unsuppressed lint or new unjustified suppressions |
| Secrets and dependencies | Pinned redacted Gitleaks source scan and reachable Go vulnerability scan; advisory dependency report for Dart |
| Architecture | Enforce allowed dependency directions, feature/public boundaries and prohibited infrastructure imports; reject cycles and contract drift |
| Unit and property tests | Deterministic public behavior, invalid inputs, financial invariants and failure recovery; Go race detection |
| Overall coverage | At least 70% of eligible handwritten production code, independently per repository |
| Incremental coverage | At least 90% of eligible added/modified executable code against the explicit merge base |
| Integration | Disposable PostgreSQL and affected HTTP lifecycle tests for persistence, auth, contract and accounting changes |
| Fuzz | Seed corpus on every unit run; bounded changed-area fuzzing on PRs and longer runs nightly |
| Mutation | Selected pure accounting/validation functions; inspect surviving mutants and retain minimized regressions |
| UI behavior | Unit/widget and native integration tests for affected routes, recovery and plugin interactions |
| UI presentation | Before/after captures for affected states, with theme, locale, viewport, text scale and device provenance |
| Independent review | Final-diff Subagent findings and resolutions, with a matching source fingerprint |

Coverage excludes generated code and test fixtures only through reviewed configuration. The denominator must include unexecuted production files. An empty eligible change set is reported as not applicable, not 100%; a missing profile or invalid explicit base is an error. Reports disclose numerator/denominator, with exclusions declared in configuration. Go overall coverage counts statements within blocks; its incremental metric counts changed source lines intersecting instrumented blocks, and every block containing an executable statement start on a counted line must have executed (using line and column coordinates). Dart LCOV counts instrumented lines. These units are not combined into one misleading cross-language percentage.

The policy started on 2026-09-16. A repository below 70% may record its measured current floor with a reason and expiry no later than 2026-10-16. This is a temporary migration exception, never an invented baseline or a waiver of the 90% incremental gate. Do not lower a baseline to accommodate a regression. Removing the exception activates the 70% floor; later increases are reviewed configuration changes.

`policy-check` compares policy with the same target revision used for coverage: thresholds cannot decrease, source roots cannot shrink, exclusions cannot be added, baseline floors/expiry cannot be weakened, and a gate step or an entire command that existed in the base policy cannot disappear without a `command_migrations` entry naming an owner, a reason and an expiry within 30 days; bumping a pinned tool version stays an ordinary change. A new exclusion requires an explicit policy migration, not an ordinary code change. Record baseline start and expiry; the maximum interval is 30 days. `skills-check` additionally requires every shared skill under `.agents/skills/` to match the template in the pinned tool bundle byte for byte, so a repository cannot quietly document a different procedure.

`security` composes Gitleaks v8.30.1 and govulncheck v1.8.0. Gitleaks scans only an isolated source copy, excludes local environment files and fully redacts findings. Govulncheck uses the current official database and fails on reachable Go vulnerabilities. Network/tool failures remain failed or blocked. Flutter's `pub outdated --json` is advisory dependency information; it does not establish comprehensive Dart vulnerability coverage or require every available upgrade.

Mutation scores do not equal correctness. The supported pure-Go target requires at least 90% killed out of killed, survived, uncovered and timed-out viable mutants, with no timeouts. Invalid mutants are reported separately and never counted as killed. Review surviving mutants, retain minimized regressions and explain equivalent mutants. An unavailable Dart adapter remains blocked, not a mutation pass. Keep budgets bounded and retain reports as CI artifacts.

## Change selection and evidence

Use an explicit base revision from the PR base SHA in CI. By default local analysis compares the working tree with HEAD and includes untracked files; this is a workspace check, not review of already committed branch changes. To review a committed branch use `just changes <target-ref>` and `just coverage-check <target-ref>`, or set `LEDGER_BASE` when running the complete gate. Changes to governance, generators, shared foundations, dependencies or public contracts broaden the checks; missing or unknown classification must fall back to the full relevant gate. The change report explains selected suites. Backend-only changes do not fabricate client screenshots, while client contract changes require both producer and consumer validation.

PR checks run the normal gate and affected expensive checks. The Go gate always includes disposable integration tests because its coverage denominator combines unit and integration evidence. Each configured Go fuzz target receives 30 seconds on an affected PR and 600 seconds nightly. Scheduled runs broaden fuzz and mutation coverage. A failed first run is not erased by a retry: retain evidence, classify flaky behavior and repair or quarantine it with an owner and expiry. UI captures complement assertions and require actual visual inspection; a golden update cannot approve itself.

Artifacts contain command, tool versions, source fingerprint, exit code and passed/failed/blocked/not-applicable status. Do not include developer `.env.local`, tokens, private financial data or raw browser authentication storage. Raw debug evidence stays ignored and local unless explicitly reviewed for sharing.

## Architecture review and debug

Mandatory rules live in `AGENTS.md`; [architecture review](../.agents/skills/ledger-architecture-review/SKILL.md) and [debug](../.agents/skills/ledger-debug/SKILL.md) skills describe execution. Mechanical checks provide a minimum floor. An independent reviewer checks architectural judgment, state ownership and failure behavior beyond those rules. A JSON report cannot authenticate a reviewer; trusted CI provenance or a human reviewer is still needed when identity assurance matters.

Debug uses competing hypotheses, the smallest reproducible case, targeted observation, a regression that fails before the fix, and the same scenario after it. Reuse slog, Go tests, Flutter integration captures and future Playwright traces. Delve/DevTools are optional deep-debug tools, not dependencies of the default gate. Debugging never authorizes production mutation or broad cleanup.

## References

- [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/)
- [Go fuzzing](https://go.dev/doc/security/fuzz/)
- [Flutter testing](https://docs.flutter.dev/testing/overview)
- [Cursor runtime-driven Debug Mode](https://cursor.com/blog/debug-mode)
- [Playwright traces](https://playwright.dev/docs/trace-viewer-intro)
