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

# Run vet + all tests + 80% coverage gate — required before every push
check: vet test
    go test -coverprofile=coverage.out ./internal/...
    go tool cover -func=coverage.out | awk '/^total/{n=$3+0; printf "Coverage: %.1f%%\n",n; if(n<80){printf "Coverage %.1f%% is below the 80%% threshold\n",n; exit 1}}'

# Run all tests
test:
    go test ./...

# Run a single package's tests verbosely (e.g. just test-pkg config)
test-pkg PKG:
    go test ./internal/{{PKG}}/... -v

# Run integration tests against the real op CLI.
# Requires: CYPHER_TEST_OP_VAULT set to an existing vault name, op CLI authenticated.
# Example: CYPHER_TEST_OP_VAULT=Private just test-integration
test-integration:
    go test -tags integration ./internal/secrets/... -v -count=1

# Run tests and print function-level coverage report (internal packages only — cmd/ is excluded)
coverage:
    go test -coverprofile=coverage.out ./internal/...
    go tool cover -func=coverage.out

# Provision a GitHub App and write credentials to .env (interactive — opens browser)
# PEM_STORAGE: "1password" (default) or "file" — controls where the App private key is stored
# OP_VAULT: 1Password vault name (default: "Private"), used when PEM_STORAGE=1password
# To override the app name when "cypher-{owner}-{repo}" is still reserved by GitHub:
#   just app_name="cypher-owner-repo-2" setup
app_name := ""
setup CONFIG="configs/project-cypher.yaml" PEM_STORAGE="1password" OP_VAULT="Private":
    go run ./cmd/cypher setup --config {{CONFIG}} --pem-storage {{PEM_STORAGE}} --op-vault {{OP_VAULT}} {{ if app_name != "" { "--app-name " + app_name } else { "" } }}

# Check the runtime environment (token, config, Docker, OpenHands, 1Password CLI if op:// secrets configured)
doctor:
    go run ./cmd/cypher doctor

# Validate a project config file (default: configs/project-cypher.yaml)
validate CONFIG="configs/project-cypher.yaml":
    go run ./cmd/cypher validate --config {{CONFIG}}

# Process the next open cypher-labelled issue and exit (requires CYPHER_GITHUB_TOKEN)
run-once:
    go run ./cmd/cypher --config configs/project-cypher.yaml

# Run the orchestrator in a continuous polling loop (requires CYPHER_GITHUB_TOKEN)
run-loop:
    go run ./cmd/cypher --config configs/project-cypher.yaml --loop

# Build and run the cypher binary with arbitrary flags
run *ARGS:
    go run ./cmd/cypher {{ARGS}}
