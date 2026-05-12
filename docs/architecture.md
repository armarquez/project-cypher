# Project Cypher: Architecture

> This document is the authoritative architecture reference for the Architect LLM and human contributors. It must be kept current — stale decisions here will directly affect the quality of architectural review. When a decision changes, update this document in the same PR as the change.

---

## 1. Vision

Project Cypher is a **project-independent, multi-agent orchestration framework** that automates the software development lifecycle: reading GitHub Issues, making code changes, running tests, and submitting Pull Requests — across any target repository, configured via a YAML file with zero code changes required per project.

---

## 2. Model Tier Strategy

The system uses three tiers with strict role boundaries.

| Tier | Models | Role | Constraint |
|---|---|---|---|
| **Architect** | Claude Sonnet / Opus | Architecture decisions, security review, OSS evaluation, PR approval | Anthropic account is limited — **never route implementation or worker tasks here** |
| **Primary Worker** | Gemini free-tier (1.5 Flash, 2.0 Flash) | Code changes, test execution, implementation tasks | Default worker path; use free-tier quota |
| **Overflow Worker** | Local LLMs (Qwen 2.5 Coder, Llama 3 via LM Studio / Ollama) | Bulk or parallel work when Gemini quota is exhausted | Zero marginal cost, GPU-local |

**Key constraint**: Claude's role is review and judgment only. Any design that routes implementation work to Claude is wrong — it defeats the cost model and exhausts the limited account.

---

## 3. Core Architecture: Control Plane vs. Data Plane

### Control Plane — Go Orchestrator

A standalone Go binary. The **only component that holds secrets** (GitHub PATs, LLM API keys for all tiers).

**Responsibilities:**
- **Secure proxy / credential gateway**: Workers cannot call LLMs or GitHub directly. All requests route through the orchestrator, which injects credentials server-side. Workers never see an API key.
- **Skill bundle assembly**: At session start, assembles worker system prompt and tool list from referenced skill bundles, then converts tool definitions to the target vendor's API schema.
- **HITL enforcement**: Detects escalation triggers, pauses agent execution, and manages the GitHub-based human approval flow.
- **Concurrency**: Goroutines manage multiple agent sessions, webhook polling, and API streaming simultaneously.

### Data Plane — OpenHands + Container

Workers run as **OpenHands** instances inside Docker containers.

**Constraints:**
- No API keys or Git credentials inside the sandbox (secretless by design)
- No outbound internet access; no host filesystem access
- Every task starts a fresh container; destroyed after PR submission (ephemeral)
- Communication is restricted to the internal gateway provided by the Control Plane

**Worker execution framework**: OpenHands (formerly OpenDevin). Runs via Docker, has a REST API, supports multiple LLM backends including Gemini.

---

## 4. Human-in-the-Loop (HITL) Protocol

HITL is enforced by the Control Plane — it is **not** delegated to the Architect LLM. The Architect may classify triggers, but the Control Plane decides whether to pause and escalate.

**Hard invariant**: No new dependencies, architectural changes, or security-impacting changes are applied without human sign-off.

### Trigger Categories

| Category | Examples |
|---|---|
| New external dependency | New package, library, third-party API; any OSS project under adoption evaluation |
| Architectural change | How components communicate, new services, data flow changes |
| Security implications | Auth, credential handling, network exposure, sandbox escape risk, trust boundary changes |

### OSS Adoption

Default posture: reuse existing open-source solutions rather than build from scratch. But adoption is never automatic.

Before adopting an OSS project, the Architect evaluates:
- Security posture: pinned deps, CVE process, maintenance activity
- Transitive dependency risk
- Abstraction fit: would we work with it or against it?

The Architect escalates to human if any of these are concerning. The human decides: **adopt fully**, **use as reference / fork**, or **build it ourselves**. This decision is recorded in the GitHub issue before any code changes.

### HITL Mechanism

1. Control Plane detects a trigger (Architect LLM classification or explicit worker output marker)
2. Opens a GitHub issue or draft PR with: what is proposed, why, alternatives considered
3. Posts a comment with the decision prompt
4. Agent session is paused
5. Human signals approval via a label, comment keyword, or issue action → session resumes

---

## 5. Skill Bundle System

Skill bundles are the **vendor-agnostic** unit of worker capability. They are an orchestrator-level concept — not Claude Code skills, not LLM-specific function libraries.

### What a Bundle Contains

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

- **`context_pack`**: Injected as a fragment into the worker's system prompt. Defines behavior, constraints, and persona for this capability area.
- **`tools`**: Tool definitions that the Control Plane converts to the target vendor's format at session start:
  - Gemini → `function_declarations`
  - Anthropic → `tools` array
  - OpenAI-compatible (Ollama, LiteLLM) → `tools` array

The same bundle works whether the worker is Gemini, a local Llama, or anything else. Vendor format conversion is entirely the Control Plane's responsibility.

### Bundle Personas

Bundles are not limited to implementation workers. The same system supports **reviewer personas** that run at Architect tier and inspect PRs rather than write code. The distinction matters for how the Control Plane routes and assembles the session:

| Persona type | Model tier | Triggered by | Example bundles |
|---|---|---|---|
| **Worker** | Gemini / Local LLM | Orchestrator dispatching a task issue | `git-operations`, `github-pr`, `go-testing` |
| **Reviewer** | Claude Sonnet/Opus | PR opened or updated webhook | `documentation-agent` |

Reviewer bundles share the same YAML format. They differ only in their `context_pack` (no implementation instructions) and tool set (read/comment tools rather than write/exec tools).

The **Documentation Agent** (`skills/documentation-agent.yaml`) is the first reviewer persona. It checks every PR for: README freshness, architecture doc updates (including C4 diagrams), skill bundle context_pack completeness, and HITL decision trail. It posts a single structured comment and does not merge or block beyond documentation gaps.

## Diagram Standards

All diagrams in this project use **MermaidJS** rendered inside fenced code blocks. ASCII art is not permitted. Use `<br />` for line breaks inside Mermaid node labels — not `\n`.

Architecture diagrams follow the **C4 model** (https://c4model.com/) using Mermaid's built-in C4 syntax (`C4Context`, `C4Container`, `C4Component`). The tooling choice is Mermaid C4 over Structurizr: it renders natively in GitHub markdown, requires no external service, and is consistent with the project's existing MermaidJS usage.

Required diagram levels:

| Change type | Required C4 level |
|---|---|
| New external actor or system dependency | Context (L1) |
| New container, process, or service | Container (L2) |
| Complex internal structure worth documenting | Component (L3) — optional |

The Documentation Agent enforces diagram presence as a hard requirement, not a suggestion.

### Bundle Assembly

The Control Plane assembles per session:
- **System prompt** = base worker persona + `context_pack` from each referenced bundle
- **Tool list** = union of `tools` from each referenced bundle, converted to target format

---

## 6. Configuration-Driven Onboarding

Adding a new target project requires only a YAML config file — no code changes.

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

## 7. Deployment Phases

### Phase 1: "Danger Room" — Current Build Target

Single-host setup on Windows + WSL2.

| Component | Where | Notes |
|---|---|---|
| LM Studio | Windows host | Serves local LLMs over network using NVIDIA GPU |
| OpenHands | Docker in WSL2 | Worker agent; native Linux context for code execution |
| Go orchestrator | WSL2 | Routes all LLM calls, manages sessions, enforces HITL |
| Gemini API | Cloud (proxied) | Primary worker model; called via orchestrator |

### Phase 2: "Krakoa" Mesh — Aspirational

Distributed across home network. **Not the current build target — do not design for it prematurely.**

- LiteLLM load-balances across: Windows GPU, Mac Mini (MLX via LM Studio `llmster`), and cloud APIs
- Ansible provisions ephemeral Incus containers per-task across network nodes
- Containers created per-task, destroyed on PR submission

---

## 8. Security Posture

| Invariant | Description |
|---|---|
| Sole secret-holder | The Go Control Plane is the only component that holds credentials. Never pass secrets into containers. |
| Secretless workers | Sandboxed agents never see an API key or Git credential. |
| Ephemeral runtimes | Every task starts with a clean container; nuked on PR submission. |
| Least privilege | GitHub tokens scoped to specific repos and branches. PR merge requires human approval. |
| HITL non-negotiable | The Control Plane enforces escalation for defined trigger categories regardless of what the Architect LLM recommends. |
