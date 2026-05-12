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

## Toolchain Management (mise + Just)

**mise** pins exact versions of all language runtimes and CLI tools. **Just** is the command runner that documents and standardizes how you interact with the project. Both are first-class citizens.

```bash
mise install   # install all tools from .mise.toml (run once after cloning)
just --list    # show all available recipes
```

`.mise.toml` is the single source of truth for tool versions (currently Go and Just). When adding any new language runtime or CLI tool, pin it in `.mise.toml` first — this keeps CI and all developer environments in sync. CI reads `.mise.toml` via `jdx/mise-action`.

---

## Key Commands

**Just** is the primary command interface. Running `just` with no arguments lists all available recipes. A local `.env` file is loaded automatically if present — use it for secrets like `CYPHER_GITHUB_TOKEN` rather than exporting them in your shell.

```bash
just                    # list all recipes (default)
just build              # compile all packages
just check              # vet + test — required before every push
just test               # run all tests
just test-pkg config    # run a single package's tests verbosely (replace 'config' with package name)
just vet                # static analysis only
just coverage           # tests + function-level coverage report
just run-once           # process next open cypher issue (requires CYPHER_GITHUB_TOKEN)
just run-loop           # continuous orchestrator loop (requires CYPHER_GITHUB_TOKEN)
```

Add new recipes to `justfile` when you find yourself running the same multi-step command more than once. Recipes serve as living documentation of how to operate the project.

---

## GitHub Issues Workflow

**All work and all follow-up items must be tracked as GitHub issues.** This includes tasks being implemented immediately, future backlog items, and anything surfaced mid-conversation that isn't acted on right now.

```bash
gh issue create --title "..." --body "..."   # create an issue
gh issue list                                 # see open issues
gh issue close <number>                       # close when work is merged
```

### When to create an issue immediately

- Before implementing any task — create the issue first, then start the branch
- When a "Future:" or "TODO:" or "we should eventually..." item comes up in conversation or review — open the issue before moving on, even if it won't be worked on for weeks
- When a PR review surfaces follow-up work — open the issue in the same session, don't rely on memory

### Why this matters

Issues are the resumable checkpoint for this project. If a session is interrupted or a future agent picks up the work, open issues show exactly what was decided and what's left. A decision that exists only in conversation history is effectively lost.

Close each issue immediately when its corresponding PR is merged — not in bulk later.

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

- **README.md**: Must reflect the current state of the project — setup steps, commands, and status. Update it in any PR that changes how the project is set up, used, or structured. It is the entry point for humans and future agents discovering the repo.
- **Architectural decisions**: Any time a meaningful design choice is made (why a component exists, why one approach was chosen over another, what constraints it operates under), it goes into `docs/architecture.md`. The Architect needs this as a reliable reference.
- **Diagrams**: All diagrams use **MermaidJS** (never ASCII art, never PlantUML). This applies to every diagram type — sequence diagrams, flowcharts, state machines, and C4 architecture diagrams. Use `<br />` for line breaks inside Mermaid node labels, not `\n`. When prose alone is insufficient to explain a flow or interaction, add an inline Mermaid diagram. Architecture diagrams follow the **C4 model** using Mermaid's built-in C4 syntax (`C4Context`, `C4Container`, `C4Component`). A new component or container requires a C4 Container diagram; a new external actor requires a C4 Context diagram. See `docs/architecture.md` for diagram standards and tooling rationale.
- **Skill bundles**: Every bundle in `/skills/` must have a clear `context_pack` that explains its purpose and constraints. A worker getting a skill bundle should understand what it can and cannot do from the bundle alone.
- **Security invariants**: Any security constraint or trust boundary must be written explicitly — not implied by code structure.
- **HITL decisions**: When a human makes a HITL decision (adopt/fork/build, approve/reject a dep), that decision and its rationale get recorded in the relevant GitHub issue or PR. This builds institutional memory the Architect can reference.
- **Per-project config**: The `/configs/<project>.yaml` is the source of truth for how a project is onboarded. It must be kept accurate.

### What does NOT need documentation

- Implementation details that are obvious from reading the code
- Transient debugging notes or workarounds (those belong in commit messages)
- Speculative future features that haven't been decided

### Updating docs

When architectural decisions change, `docs/architecture.md` must be updated in the same PR as the change. When user-facing behavior changes (new commands, new setup steps, status changes), `README.md` must be updated in the same PR. Stale docs are worse than no docs — the Architect and future agents will make decisions based on whatever is written down.

### Documentation Agent

The **Documentation Agent** (`skills/documentation-agent.yaml`) is a reviewer persona that checks every PR for documentation completeness: README freshness, architecture doc updates, C4 diagram presence, skill bundle context_pack quality, and HITL decision trail. It runs at Architect tier and posts a single structured PASS / NEEDS WORK comment. The PR checklist (`.github/pull_request_template.md`) remains the manual enforcement mechanism until the agent is wired into the PR webhook flow.

---

## Security Invariants

These must be preserved in all implementations:

1. The Go Control Plane is the **sole secret-holder** — never pass credentials into worker containers
2. Worker sandboxes are **ephemeral** — created per-task, destroyed after PR submission
3. GitHub tokens are **scoped** to specific repos and branches; PR merge approval requires human sign-off
4. All LLM calls from the sandbox are **intercepted and proxied** by the Control Plane (credential injection happens server-side)
5. **HITL is not optional** — the Control Plane must enforce escalation for the defined trigger categories regardless of what the Architect LLM recommends
