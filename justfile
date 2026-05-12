# Project Cypher — AI-driven orchestration engine
# https://github.com/armarquez/project-cypher

# Automatically load a local .env file if present (e.g. CYPHER_GITHUB_TOKEN=...)
set dotenv-load := true

# List available commands (default when `just` is run with no arguments)
default:
    @just --list --unsorted

# Build the cypher binary
build:
    go build ./...

# Run static analysis
vet:
    go vet ./...

# Run vet + all tests — required before every push
check: vet test

# Run all tests
test:
    go test ./...

# Run a single package's tests verbosely (e.g. just test-pkg config)
test-pkg PKG:
    go test ./internal/{{PKG}}/... -v

# Run tests and print function-level coverage report
coverage:
    go test -coverprofile=coverage.out ./...
    go tool cover -func=coverage.out

# Process the next open cypher-labelled issue and exit (requires CYPHER_GITHUB_TOKEN)
run-once:
    go run ./cmd/cypher --config configs/project-cypher.yaml

# Run the orchestrator in a continuous polling loop (requires CYPHER_GITHUB_TOKEN)
run-loop:
    go run ./cmd/cypher --config configs/project-cypher.yaml --loop

# Build and run the cypher binary with arbitrary flags
run *ARGS:
    go run ./cmd/cypher {{ARGS}}
