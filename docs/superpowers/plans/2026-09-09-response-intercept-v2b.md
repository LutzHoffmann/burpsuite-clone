# Response Intercept v2b Implementation Plan

Goal: implement the approved v2b design end to end.
Spec: docs/superpowers/specs/2026-09-09-response-intercept-v2b-design.md
Architecture: shared preparation stages, independent interception controllers and
ordered bounded replacements; existing Go/SQLite backend and React frontend.

## Task 1: Proxy and interception engine

Ownership: internal/intercept and internal/proxy.
Implement the spec contract, response queue, validated bounded replacement rules
and shared HTTP/HTTPS preparation. Write behavioral tests for regex ordering,
scope escape, response edits/drop and byte-preserving bypass. Run package tests.
Consume store.Exchange.AppliedRuleIDs and ResponseIntercepted from Task 2.

## Task 2: Persistence, API and startup

Ownership: internal/store, internal/api, cmd/proxy.
Migrate and round-trip audit fields; implement response queue endpoints and DTOs;
validate config before persistence and activate only after successful persistence.
Restore all new configuration on startup. Test malformed edits, invalid regex,
config failures, persistence and old schema compatibility. Coordinate against the
exact controller and DTO contract in the spec.

## Task 3: Operator UI

Ownership: web/src.
Provide independent response switch and rule controls, response editor with status,
headers/body and drop. Add ordered replacement rule editor supporting regex and
literal matches, enable/disable and movement; preserve existing request controls.
Show audit in Inspector. Test API/UI interactions and errors; run tests and build.

## Task 4: Integration and review

Ownership: coordinator, docs and integration refinements.
Review all interfaces and whole-system behavior. Run full Go suite, race checks,
vet, frontend suite/build and Windows compile. Fix evidenced failures and review
findings. Document limits and operations. Commit the verified change; report
results and any residual gaps accurately.

## Progress

- Design and interface review complete; tasks 1-3 use disjoint file ownership.
- Integration dependencies: controller contract Task 1 -> Task 2; JSON API Task 2
  -> Task 3; exchange audit fields Task 2 -> Task 1. Contracts defined in spec.
- Ruling: proceed with approved defaults, avoiding repeated confirmation of
  reversible implementation details.
- Tasks 1-3: implemented. Delegated workers stopped at their usage limit; the
  coordinator reviewed and completed the integration and remaining fixes.
- Task 4: local verification complete: Go suite, race checks, vet, 80 frontend
  tests, production build, and full Windows cross-compilation (without executing
  Windows tests). Browser checks verified rule saving/reloading and a 390px view.
- Integration tests exercise real HTTP/HTTPS through API and SQLite, manual
  response edits/drop, scope escape rejection, audit, and preservation of binary,
  gzip, oversized and HEAD responses.
- Review fixes: queue header cloning, rule ordering when earlier rules enable
  later body filters, noneditable body forwarding, bounded regex match/capture
  metadata, and standard Unicode replacement-template semantics.
- Independent final agent review was unavailable after the usage-limit stop;
  coordinator code review and executable verification were performed instead.
