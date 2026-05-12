# Project Cypher — command runner
# Run `just --list` to see all available recipes.

# Build the cypher binary
build:
    go build ./...

# Run all tests
test:
    go test ./...

# Run tests for a specific package verbosely (e.g. just test-pkg config)
test-pkg PKG:
    go test ./internal/{{PKG}}/... -v

# Run static analysis
vet:
    go vet ./...

# Run vet + tests — use this before pushing
check: vet test

# Run tests with coverage report (use just coverage in CI or before a PR)
coverage:
    go test -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out

# Build and run the cypher binary
run *ARGS:
    go run ./cmd/cypher {{ARGS}}
