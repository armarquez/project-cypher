# Project Cypher

Multi-agent orchestration framework that automates the software development lifecycle — reading GitHub Issues, making code changes, running tests, and submitting Pull Requests across any target repository.

Onboarding a new project requires no code changes. Drop a YAML file in `configs/` and Cypher handles the rest.

## Architecture

Cypher uses a two-plane design:

- **Control Plane** (Go orchestrator) — the only component that holds secrets. Acts as a secure proxy: workers route all LLM calls and GitHub API calls through it, which injects credentials server-side.
- **Data Plane** (OpenHands in Docker) — secretless, isolated worker agents. Ephemeral per task.

LLM tiers:
| Role | Model |
|---|---|
| Architect (review, security, arch decisions) | Claude Sonnet / Opus |
| Primary Worker (implementation) | Gemini free-tier |
| Overflow Worker | Local LLMs via LM Studio / Ollama |

See [`docs/architecture.md`](docs/architecture.md) for the full architecture reference.

## Getting Started

**Prerequisites**: [mise](https://mise.jdx.dev) installed on your system.

```bash
git clone git@github.com:armarquez/project-cypher.git
cd project-cypher
mise install          # installs Go 1.24.6 and just 1.51.0
just check            # vet + all tests — confirm everything is green
```

## Common Commands

```bash
just build            # compile the cypher binary
just check            # vet + tests (run before every push)
just test-pkg config  # run a single package's tests verbosely
just run              # build and run the cypher binary
```

Run `just --list` for the full list of recipes.

## Project Structure

```
cmd/cypher/          # binary entry point
internal/config/     # per-project config loader
internal/skills/     # skill bundle loader, assembler, and vendor format converters
configs/             # per-project YAML configs (example.yaml included)
skills/              # skill bundle YAML definitions
docs/                # architecture and design documentation
```

## Status

Early development — Phase 1 ("Danger Room") underway. Sprint 1 complete:
- Config loader (`internal/config`)
- Skill bundle loader with vendor format conversion for Gemini, Anthropic, and OpenAI-compatible LLMs (`internal/skills`)

Next: HTTP gateway (Control Plane), GitHub client, OpenHands session management.
