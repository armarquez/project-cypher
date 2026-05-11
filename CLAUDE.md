# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**Project Cypher** is a multi-agent orchestration framework that automates the software development lifecycle: reading GitHub Issues, making code changes, running tests, and submitting Pull Requests — across any target repository, with zero code changes required per project.

The full architectural sketch is in `docs/architecture.md`, but treat it as a starting point, not a north star. It was AI-generated (Gemini Pro) and several of its assumptions are revised here.

---

## Engineering Principles

**Best practices over velocity.** We optimize for long-term maintainability and correctness, not for getting things done fast. This means:
- Follow idiomatic conventions for the language/framework in use — don't invent patterns when the ecosystem already has one
- Prefer explicit, readable code over clever code
- Do not skip steps (validation, error handling at system boundaries, tests) to ship faster

For Go specifically: idiomatic error wrapping (`fmt.Errorf("context: %w", err)`), table-driven tests, no global state, package-level types that are self-documenting. Do not use `init()` functions or `panic` except in `main`.

---

## Toolchain Management (mise)

**mise** is the primary tool manager for this project. It pins exact versions of all language runtimes and CLI tools so that local dev and CI use identical environments.

```bash
mise install        # install all tools pinned in .mise.toml (run after cloning)
mise use go@1.24.6  # update the pinned Go version in .mise.toml
```

`.mise.toml` at the repo root is the single source of truth for tool versions. When adding a new language runtime or CLI tool to the project, add it here first. CI reads `.mise.toml` via `jdx/mise-action`.

---

## Key Commands

```bash
go build ./...   # build all packages
go vet ./...     # static analysis
go test ./...    # run all tests
go test ./internal/config/... -v   # run a single package's tests verbosely
```

---

## Branch and PR Workflow

**Never commit directly to `main`.** All work goes through a short-lived branch and a PR. Main is protected by a GitHub Ruleset that requires:
- A PR to be open (no direct pushes)
- The `ci` check to pass
- No required human approvals for now — self-merge once CI is green. This will change when we integrate HITL into the merge gate.

### Branch naming
Use `type/short-description`:
- `feat/` — new feature, new component, new skill bundle
- `fix/` — bug fix, broken config
- `infra/` — CI, repo tooling, docs, CLAUDE.md

### Standard PR flow
```bash
git checkout -b feat/my-thing
# ... do work, commit ...
git push -u origin feat/my-thing
gh pr create --title "..." --body "..."
gh run watch                              # wait for CI
gh pr merge --squash --delete-branch     # merge once green
```

Always squash-merge so main history stays one commit per feature/fix.

---

## Model Tier Strategy

The system uses three tiers. **Never route implementation tasks to Claude** — the Anthropic account is limited and reserved exclusively for the Architect role.

| Tier | Models | Role |
|---|---|---|
| **Architect** | Claude Sonnet / Opus | Architecture decisions, security review, OSS evaluation, PR approval |
| **Primary Worker** | Gemini (free-tier: 1.5 Flash, 2.0 Flash) | Code changes, test execution, implementation tasks |
| **Overflow Worker** | Local LLMs (Qwen 2.5 Coder, Llama 3 via LM Studio / Ollama) | Bulk or parallel work when Gemini quota is exhausted |

The architecture doc assumed workers = local LLMs only. That is superseded: **Gemini is the primary worker**. Local LLMs are the overflow path.

---

## Architecture

### Control Plane (Go Orchestrator)

A standalone Go binary — the only component that holds secrets (GitHub PATs, LLM API keys). All agent communication routes through it.

- **Secure proxy**: Workers cannot call LLMs or GitHub directly; they must go through the Control Plane, which injects credentials server-side.
- **Skill bundle assembly**: On each worker session start, the Control Plane builds the system prompt and tool list from referenced skill bundles and converts tool definitions to the target vendor's schema (see Skill Bundles below).
- **HITL gate**: Detects escalation triggers and pauses agent execution pending human approval.
- Uses goroutines for concurrent session management, webhook polling, and streaming.

### Data Plane (OpenHands + Container)

Workers run as OpenHands instances inside Docker containers (WSL2 for Phase 1).

- **Secretless**: No API keys or Git credentials inside the sandbox. All LLM calls route through the Control Plane gateway.
- **Isolated**: No outbound internet, no host filesystem access.
- **Ephemeral**: Each task starts a fresh container; destroyed after the PR is submitted.

### Deployment Phases

**Phase 1 ("Danger Room") — current target:**
- LM Studio on Windows host (NVIDIA GPU, serves local LLMs to network)
- OpenHands in Docker inside WSL2
- Go orchestrator in WSL2
- Gemini API calls proxied through the Go orchestrator

**Phase 2 ("Krakoa" mesh) — aspirational:**
- LiteLLM load-balances across Windows GPU, Mac Mini (MLX via LM Studio llmster), and cloud APIs
- Ansible provisions ephemeral Incus containers per-task across network nodes
- Phase 2 is not the current build target. Don't design for it prematurely.

---

## Human-in-the-Loop (HITL) Protocol

HITL is a hard gate — it is **not** delegated to the Architect LLM. It escalates directly to the human. No new dependencies, architectural changes, or security-impacting changes are applied without human sign-off.

### Trigger Categories

Any one of these is sufficient to pause and escalate:

- **New external dependency** — any new package, library, or third-party API being added, including OSS projects under evaluation for adoption
- **Architectural change** — how components communicate, new services introduced, data flow changes
- **Security implications** — auth, credential handling, network exposure, sandbox escape risk, anything touching the trust boundary

### OSS Adoption Evaluation

The default posture is to find and reuse existing open-source solutions rather than build from scratch. But adoption is never automatic. Before pulling in an OSS project, the Architect evaluates it and escalates to the human if:

- The project doesn't follow security best practices (no pinned deps, no CVE process, effectively unmaintained)
- Adoption would introduce an unacceptable transitive dependency chain
- The project's design would require working against it (poor abstraction fit)

The human decides: **adopt fully**, **adopt partially** (reference implementation / fork), or **build it ourselves**. This decision happens before any code change, not after.

### Mechanism

1. Control Plane detects a trigger (via Architect LLM classification or an explicit marker in worker output)
2. Opens a GitHub issue or draft PR with full context: what is being proposed, why, and what alternatives were considered
3. Posts a comment with the decision prompt and available options
4. Agent session is paused; resumes only when the human signals approval via a label, comment keyword, or issue action

---

## Skill Bundle System

Skill bundles are the vendor-agnostic way to define worker capabilities. They live in `/skills/` and are referenced per-project from `/configs/`.

### Bundle Format

```yaml
# skills/git-operations.yaml
name: git-operations
context_pack: |
  You have access to git tools for reading repository state and staging changes.
  Never force-push. Never commit directly to main.
tools:
  - name: git_status
    description: "Run git status in the sandbox repo"
    parameters: {}
    impl: sandbox_exec
  - name: git_diff
    description: "Show diff of staged or unstaged changes"
    parameters:
      staged:
        type: boolean
    impl: sandbox_exec
```

- `context_pack` — injected as a fragment into the worker's system prompt
- `tools` — tool definitions that the Control Plane converts to the target vendor's schema at session start:
  - Gemini → `function_declarations`
  - Anthropic → `tools` array
  - OpenAI-compatible (Ollama, LiteLLM) → `tools` array

This means the same skill bundle works regardless of which LLM tier the worker is using. "Skills" in this system are **not** Claude Code CLI skills — those are Claude Code-specific. Skill bundles here are an orchestrator-level concept.

### Per-Project Config

```yaml
# configs/backend-service.yaml
target_repo: https://github.com/org/repo-name
worker_model: gemini/gemini-2.0-flash
architect_model: anthropic/claude-sonnet-4-5
test_command: go test ./...
skills:
  - git-operations
  - github-pr
  - go-testing
design_constraints: "No global state; all functions must be unit-testable."
```

---

## Documentation Requirements

Documentation is a first-class concern in this project. The Architect LLM makes decisions based on what is written down — if a decision isn't documented, it effectively doesn't exist from the Architect's perspective.

### What must always be documented

- **Architectural decisions**: Any time a meaningful design choice is made (why a component exists, why one approach was chosen over another, what constraints it operates under), it goes into `docs/architecture.md`. The Architect needs this as a reliable reference.
- **Skill bundles**: Every bundle in `/skills/` must have a clear `context_pack` that explains its purpose and constraints. A worker getting a skill bundle should understand what it can and cannot do from the bundle alone.
- **Security invariants**: Any security constraint or trust boundary must be written explicitly — not implied by code structure.
- **HITL decisions**: When a human makes a HITL decision (adopt/fork/build, approve/reject a dep), that decision and its rationale get recorded in the relevant GitHub issue or PR. This builds institutional memory the Architect can reference.
- **Per-project config**: The `/configs/<project>.yaml` is the source of truth for how a project is onboarded. It must be kept accurate.

### What does NOT need documentation

- Implementation details that are obvious from reading the code
- Transient debugging notes or workarounds (those belong in commit messages)
- Speculative future features that haven't been decided

### Updating docs

When architectural decisions change, `docs/architecture.md` must be updated in the same PR as the change. Stale architecture docs are worse than no docs — the Architect will make decisions based on outdated information.

---

## Security Invariants

These must be preserved in all implementations:

1. The Go Control Plane is the **sole secret-holder** — never pass credentials into worker containers
2. Worker sandboxes are **ephemeral** — created per-task, destroyed after PR submission
3. GitHub tokens are **scoped** to specific repos and branches; PR merge approval requires human sign-off
4. All LLM calls from the sandbox are **intercepted and proxied** by the Control Plane (credential injection happens server-side)
5. **HITL is not optional** — the Control Plane must enforce escalation for the defined trigger categories regardless of what the Architect LLM recommends
