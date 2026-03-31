---
applyTo: "**/*_test.go"
---
# Testing Guidelines

## Test-Driven Development
This project follows strict TDD. The commit sequence is:
1. `test: add failing tests for <feature>` — write the test first
2. `feat: implement <feature>` — minimum code to make it pass
3. `refactor: clean up <feature>` — improve without changing behaviour

## Test Structure
- Use table-driven tests with `t.Run(name, func(t *testing.T) { ... })`
- Name subtests descriptively: `TestChain_ProcessRequest/single_middleware`
- Place `_test.go` files in the same package as the code under test
- For internal-only test helpers, use `_internal_test.go` (e.g., `manager_internal_test.go`)

## Test Utilities (`internal/testutil/helpers.go`)
- `testutil.SetupTestServer(t)` — spins up a mock Anthropic-compatible HTTP server
- `testutil.AssertJSONEqual(t, expected, actual)` — deep JSON comparison ignoring key order
- `testutil.LoadTestData(t, "filename.json")` — loads fixture from `internal/testutil/testdata/`
- `testutil.MustMarshal(t, v)` — JSON-marshals or fails the test

## Coverage
- CI enforces a minimum of 45% total coverage
- Always run tests with race detection: `go test -race -count=1 ./...`
- Generate coverage report: `go test -race -coverprofile=coverage.out ./...`

## Assertions
- Use `t.Fatalf` for conditions that prevent further test execution
- Use `t.Errorf` for conditions that should be reported but allow the test to continue
- Prefer specific assertion messages: `t.Errorf("ResolveModelID(%q) = %q, want %q", input, got, want)`

## Mocking
- Use `httptest.NewServer` for HTTP endpoint mocks
- Use package-level variable test seams (e.g., `uuidEntropySource = func(b []byte) (int, error) { ... }`)
- Restore original values in `t.Cleanup` to avoid test pollution
