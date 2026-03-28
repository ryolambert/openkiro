# Contributing to openkiro

Welcome! openkiro follows a strict **Test-Driven Development (TDD)** workflow. Every feature PR must start with failing tests before any implementation is written.

## Table of Contents

- [TDD Policy](#tdd-policy)
- [PR & Commit Sequence](#pr--commit-sequence)
- [Coverage Requirements](#coverage-requirements)
- [Running Tests Locally](#running-tests-locally)
- [Middleware Development](#middleware-development)
- [Related Issues](#related-issues)

---

## TDD Policy

All feature work follows the **Red → Green → Refactor** cycle:

1. **Red** — Write a failing test that precisely describes the desired behaviour. Commit it with `test: add failing tests for <feature>`. The CI pipeline will show this test as failing — that is expected and correct.
2. **Green** — Write the _minimum_ production code needed to make the failing test pass. Commit with `feat: implement <feature>`.
3. **Refactor** — Clean up duplication, improve names, add table-driven tests, ensure race detection passes. Commit with `refactor: clean up <feature>`.

**No production code is accepted without a corresponding test.** PRs that skip directly to implementation without first showing a red test will be asked to restructure.

### Why TDD?

openkiro sits between every AI tool call and the Kiro/CodeWhisperer backend. Regressions here break the entire developer experience. TDD ensures every edge case is explicitly specified before code is written.

---

## PR & Commit Sequence

Use [Conventional Commits](https://www.conventionalcommits.org/) throughout:

```
test: add failing tests for NoopMiddleware
feat: implement NoopMiddleware and Chain
refactor: table-driven tests for Chain
```

The recommended PR structure mirrors this three-commit sequence. Squash merges are **not** used so the red/green/refactor history is visible in `git log`.

---

## Coverage Requirements

| Threshold | Status |
|-----------|--------|
| **50 %** | Minimum (enforced by CI — PRs below this threshold will fail) |

Coverage is measured with `go test -race -coverprofile=coverage.out ./...` and uploaded as a CI artifact on every push to `main` and on every PR.

The threshold is a ratchet: it only goes up. Once coverage exceeds the current minimum, the minimum is raised to match.

---

## Running Tests Locally

```bash
# Run all tests with race detection
go test -race ./...

# Generate a coverage report
go test -race -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
open coverage.html

# Run tests for a specific package
go test -race -v ./internal/middleware/...

# Run only tests whose name matches a pattern
go test -race -run TestChain ./internal/middleware/...

# Lint (requires golangci-lint)
golangci-lint run

# All quality gates (lint + test)
make check
```

---

## Middleware Development

New middleware must:

1. Implement the `middleware.Middleware` interface defined in `internal/middleware/middleware.go`:
   ```go
   type Middleware interface {
       ProcessRequest(req *proxy.AnthropicRequest) (*proxy.AnthropicRequest, error)
       ProcessResponse(resp []byte) ([]byte, error)
       Name() string
   }
   ```
2. Live in `internal/middleware/<name>.go` alongside `<name>_test.go`.
3. Follow the TDD cycle — see `internal/middleware/middleware_test.go` for the canonical example demonstrating red/green/refactor.
4. Use test fixtures from `internal/testutil/testdata/` via `testutil.LoadTestData(t, "filename.json")`.
5. Register with the proxy `Chain` in the appropriate server setup code.

### Test Helpers

`internal/testutil/helpers.go` provides four utilities:

| Function | Purpose |
|----------|---------|
| `SetupTestServer(t)` | Spins up a mock Anthropic-compatible HTTP server |
| `AssertJSONEqual(t, expected, actual)` | Deep JSON comparison ignoring key order / whitespace |
| `LoadTestData(t, name)` | Loads a fixture from `internal/testutil/testdata/` |
| `MustMarshal(t, v)` | JSON-marshals a value, failing the test on error |

---

## Related Issues

- **Parent epic**: [openkiro proxy expansion #1](https://github.com/ryolambert/openkiro/issues/1)
- **TDD & CI Foundations**: the issue this document was created for
- Downstream: rtk integration, icm context injection, ToolOptimizer, Docker MCP Gateway, Docker Sandbox microVMs — all depend on this foundation
