---
name: ledger-architecture-review
description: Independently review Ledger architecture and final code changes against dependency, contract, state ownership and evidence rules. Use for required delivery reviews and design reviews of boundary, public contract or shared component changes.
---

# Independent architecture review

Read `AGENTS.md`, `docs/architecture.md` when present, `docs/governance.md` and affected contracts. Delegate to a separate Subagent that has not implemented the reviewed change. Supply requirements, base revision and validation evidence; the reviewer independently inspects the diff and callers. Do not delegate only a predetermined verdict.

For dependency-boundary, API/schema or shared-component changes, review the proposed ownership before implementation. Review the final source again before delivery.

- Check single ownership of decisions/state, allowed dependency direction, and domain/tenant/transport boundaries.
- Justify abstractions with present callers; minimize public surface and hidden coupling.
- Verify exact money, stable errors, versioning, recovery and cancellation contracts.
- Challenge unjustified dependencies, suppressions, generation and quality exclusions.
- Require behavior-based regressions and actual native/visual evidence for corresponding claims.

The reviewer writes the tracked `.governance/review.json` so CI can check it:

```json
{
  "schema_version": 1,
  "fingerprint": "current ledger-tool fingerprint output",
  "reviewer": "actual independent reviewer identifier",
  "independent": true,
  "scope": ["reviewed paths or boundaries"],
  "findings": [{
    "severity": "P1",
    "file": "relative/source.go",
    "line": 1,
    "rule": "specific contract or invariant",
    "trigger": "concrete failing scenario",
    "impact": "observable consequence",
    "recommendation": "smallest corrective change",
    "resolved": false
  }],
  "limitations": ["checks or environments not independently verified"]
}
```

Use an empty findings array if no actionable defect is found. P0/P1 findings block delivery until corrected or disproved with evidence. Distinguish advice from defects. The implementer cannot manufacture reviewer identity, independence or findings.

Run `just review-check`. If code changes, the independent reviewer rechecks affected behavior and issues a report for the new fingerprint. Report files do not enter their own fingerprint. A passing report check proves schema/freshness only; authentic identity requires trusted runner provenance or human review. Without an independent reviewer, report the review as blocked rather than self-approving.
