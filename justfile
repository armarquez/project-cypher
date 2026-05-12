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

# Run orchestrator once against the project-cypher config (requires CYPHER_GITHUB_TOKEN)
run-once:
    go run ./cmd/cypher --config configs/project-cypher.yaml

# Run orchestrator in continuous loop (requires CYPHER_GITHUB_TOKEN)
run-loop:
    go run ./cmd/cypher --config configs/project-cypher.yaml --loop

# Build and run the cypher binary with arbitrary args
run *ARGS:
    go run ./cmd/cypher {{ARGS}}
