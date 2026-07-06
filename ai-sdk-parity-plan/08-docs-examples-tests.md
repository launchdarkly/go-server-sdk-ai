# Task 08 — Docs, examples, and test/coverage parity

**Depends on:** all preceding tasks (01–07). Do the doc/example work for each
capability as it lands, or as a consolidation pass at the end.

## Objective

Round out the release: package docs, README updates, runnable examples, and a
final sweep to ensure test coverage and lint parity with the other SDKs.

## Spec references

- Whole `AISDK-ai-sdk` spec tree for user-facing accuracy.
- `sdk-specs/specs/EXAM-SDK-example` (Hello App standard) — reference only; a
  full hello app is optional unless maintainers want it.

## Reference implementations

- .NET `pkgs/sdk/server-ai` README + XML doc comments.
- Java `lib/sdk/server-ai` package-info + Javadoc.
- Existing Go `README.md` "Getting started" section (completion example).

## Work items

1. **README.md**: extend "Getting started" with short snippets for
   `AgentConfig` / `AgentConfigs`, the `*Template` methods, `AgentGraph` +
   traversal, and graph tracking. Keep the pre-release CAUTION banner. Ensure
   the "Active feature development is ongoing in Python/Node" note is still
   accurate (this SDK is catching up — reword if appropriate, confirm with
   maintainers).
2. **Go doc comments**: every new exported type/method needs a doc comment
   (golangci-lint / `revive` style already enforced in this repo). Reference the
   spec requirement number where behavior is subtle (mode validation, at-most-
   once, resumption-token ordering, evaluator noop).
3. **Examples**: if the repo adds runnable examples (check for an `examples/` or
   provider sub-module convention in CONTRIBUTING "Code organization"), add one
   per new capability. Otherwise embed `Example*` test functions
   (`example_test.go`) so `go doc` renders them — the idiomatic Go approach.
4. **Coverage sweep**: run `go test -race -coverprofile=coverage.out ./...` and
   `go tool cover -func=coverage.out`; ensure new files are meaningfully
   covered and there are no untested spec requirements. Match the coverage
   discipline of `client_test.go` / `tracker_test.go` / `judge/judge_test.go`.
5. **Lint**: `golangci-lint run` clean across the module (and any new
   sub-module `go.mod` if one is added).
6. **Contract tests (assess):** the spec ecosystem uses a shared SDK
   test-harness / contract-test service for parity verification. Check whether
   the other `server-ai` SDKs wire an AI contract-test service and whether this
   repo is expected to add a `testservice/` (the base `go-server-sdk` has one).
   If in scope, file a follow-up; it is likely a separate effort from this
   parity work. Flag to maintainers rather than silently expanding scope.
7. **CHANGELOG / release**: releases here are driven by conventional-commit PR
   titles (release-please style). Ensure each capability PR uses a `feat:`
   title so the minor version bumps; breaking changes (e.g. if task 01 chose a
   non-backward-compatible typed-config split) use `feat!:` and are called out.

## Acceptance criteria

- README + doc comments cover every new public API with accurate, spec-aligned
  examples.
- `go build ./...`, `go test -race ./...`, `golangci-lint run` all clean.
- New code is covered by tests; no spec requirement in scope is left untested.
- Any contract-test / hello-app gap is documented as a tracked follow-up rather
  than left implicit.
