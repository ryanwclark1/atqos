# Autonomous Test & Quality Orchestration System (ATQOS)

## 1. Purpose & Vision

The purpose of this project is to design and build a **language-agnostic, non-interactive orchestration system** that can automatically:

- Diagnose quality issues in a codebase (initially Python/pytest)
- Plan and prioritize corrective actions
- Delegate fixes to one or more code-capable agents (LLM-based or otherwise)
- Validate results deterministically
- Iterate safely with bounded retries
- Produce auditable artifacts and reports

The long-term vision is a **generalized quality remediation engine** that supports testing, coverage, linting, type checking, formatting, and eventually multiple ecosystems (Python, TypeScript, Go, etc.), without being tightly coupled to any single language, framework, or toolchain.

The system must be:

- Portable (single binary where possible)
- Safe (no dependency pollution of target projects)
- Deterministic (bounded retries, clear stop conditions)
- Extensible (plugin-based architecture)
- Observable (rich artifacts, logs, and metrics)

---

## 2. Initial Scope (v1)

### 2.1 In Scope

**Primary focus (v1):**

- Python projects using `pytest`
- Test execution, failure analysis, and automated remediation
- Coverage analysis and test generation to meet coverage targets
- Parallel agent execution with coordination and validation

**Core capabilities:**

- Run pytest in a non-interactive, automated fashion
- Collect structured test results (JSON, JUnit)
- Categorize and prioritize failures
- Assign remediation tasks to agents
- Validate fixes via targeted test runs
- Enforce retry limits and escalation rules
- Maintain a full audit trail of actions and results

### 2.2 Explicitly Out of Scope (for v1)

- Human-in-the-loop workflows
- Interactive UIs (CLI is sufficient)
- Full CI/CD integration (design should allow it later)
- Automatic production deployments
- Non-test-related refactors (unless required to fix tests)

---

## 3. Non-Goals (Important Constraints)

The system will **not**:

- Import or execute target project code directly
- Install dependencies into the orchestrator runtime
- Modify target project dependencies unless explicitly instructed
- Run indefinitely or attempt unlimited fixes
- Hide or auto-delete failing or obsolete tests without traceability

---

## 4. Architectural Principles

### 4.1 Strong Environment Isolation

- The orchestrator is implemented in **Go** and distributed as a standalone binary
- Target projects are treated as black boxes
- All test, coverage, lint, or build commands are executed via explicit subprocess calls
- The orchestrator never shares a Python/Node environment with the target project

### 4.2 Plugin-Based Tooling Model

All quality tools (pytest, coverage, ruff, mypy, vitest, etc.) are implemented as **plugins** that conform to a shared lifecycle:

1. Collect
2. Normalize
3. Plan
4. Validate

This ensures that adding new tools or ecosystems does not require rewriting core orchestration logic.

### 4.3 Deterministic Orchestration

- All actions are represented as explicit tasks
- Tasks have bounded retries
- Progress is measured quantitatively
- Clear stop conditions prevent infinite loops

---

## 5. Core System Components

### 5.1 Orchestrator (Go Binary)

Responsibilities:

- Manage run lifecycle
- Maintain task state
- Schedule and execute workers
- Coordinate agents
- Enforce retry and escalation policies
- Emit structured logs and artifacts

The orchestrator must be:

- Deterministic
- Crash-resilient
- Restartable using persisted state

### 5.2 Runner Abstraction

A generic execution layer responsible for invoking external tools.

Capabilities:

- Execute commands with explicit environment and working directory
- Stream stdout/stderr to artifact files
- Enforce timeouts and cancellation
- Return structured execution results

Example runners:

- PythonRunner (pytest, coverage, mypy, ruff)
- NodeRunner (vitest, eslint)
- GenericCommandRunner

### 5.3 Plugin Interface

Each plugin implements:

- **Collect**: Run tool and generate raw artifacts
- **Normalize**: Parse raw output into canonical Findings
- **Plan**: Convert Findings into prioritized Tasks
- **Validate**: Define commands that verify task success

Plugins must not mutate global orchestrator state directly.

---

## 6. Canonical Data Models

### 6.1 Finding

A Finding represents a normalized diagnostic issue, independent of the underlying tool.

Attributes:

- tool (pytest, coverage, ruff, mypy, etc.)
- kind (test_failure, coverage_gap, lint_error, type_error)
- severity (blocker, high, medium, low)
- scope (file, symbol, test identifier)
- fingerprint (stable hash for deduplication)
- message (human-readable summary)
- artifact references (raw logs, JSON offsets)

### 6.2 Task

A Task represents a unit of work assigned to an agent.

Attributes:

- task_type (fix, add_test, remove_skip, refactor, suppress)
- tool
- targets (files, test nodes)
- validation commands
- retry policy
- status (queued, running, succeeded, blocked, abandoned)

### 6.3 Validation Result

Uniform across tools:

- exit code
- summary counts
- delta from baseline (optional)
- artifact references

---

## 7. Agent Integration Model

### 7.1 Agent Contract

Agents are invoked non-interactively and must:

- Receive explicit instructions and file scope
- Apply changes only within allowed paths
- Run specified validation commands
- Emit a structured JSON result to stdout

### 7.2 Supported Agents (Initial)

- Codex CLI
- Claude Code
- Cursor Agent

Agents are treated as interchangeable execution backends.

---

## 8. Task Scheduling & Concurrency

### 8.1 Worker Pool

- Configurable number of concurrent workers
- Separate limits for:

  - Tool execution (pytest, coverage)
  - Agent execution

### 8.2 Task Granularity

Initial heuristics:

- Test failures: one test file per task
- Coverage gaps: one source module per task
- Skip audits: one test file per task

### 8.3 Git Isolation Strategy

Preferred approach:

- Per-task branches or worktrees
- All changes committed with task metadata
- Central orchestrator applies validated changes

---

## 9. Retry, Escalation & Stop Conditions

### 9.1 Retry Policy

- Default: 2 attempts per task
- Systemic issues: up to 3 attempts

### 9.2 Escalation Rules

- Repeated identical failures create a systemic task
- Systemic tasks block dependent tasks

### 9.3 Stop Conditions

- No net reduction in failures across checkpoints
- Repeated identical fingerprints
- Excessive diff size or file churn

---

## 10. Observability & Artifacts

### 10.1 Artifact Layout

Each run produces an immutable artifact directory containing:

- events.jsonl (append-only event log)
- raw tool outputs
- normalized reports
- diffs and patches
- agent prompts and responses

### 10.2 State Persistence

- SQLite database
- Tables for runs, findings, tasks, attempts, artifacts
- Enables resume and post-run analysis

### 10.3 Metrics (Future)

- Prometheus-compatible metrics endpoint
- Task throughput, failure counts, retry rates

---

## 11. Coverage Workflow (v1)

1. Run baseline coverage
2. Identify low-coverage modules
3. Prioritize by criticality and churn
4. Assign test-writing tasks
5. Validate coverage improvement per task
6. Full coverage checkpoint

Coverage work is sequenced after test stability by default.

---

## 12. Skip & Obsolete Test Handling

### 12.1 Skip Auditing

- Static detection of skip/skipif markers
- Agent-driven reasoning on validity
- Optional execution with temporary override plugin

### 12.2 Obsolete Test Detection

Heuristics include:

- Dead imports or removed APIs
- Zero-execution coverage
- Repeated permanent skips

Tests are quarantined before deletion.

---

## 13. Extensibility Roadmap (Non-Binding)

Planned future plugins:

- Ruff (lint + format)
- Mypy / Pyright / Ty (type checking)
- Vitest (TypeScript)
- ESLint / Prettier
- Go test

Core architecture must not assume Python-specific semantics.

---

## 14. Success Criteria

The project is considered successful when:

- It can autonomously reduce pytest failures in a real-world project
- It improves coverage measurably without human intervention
- It produces auditable, reproducible artifacts
- It can be extended to at least one additional tool without core rewrites

---

## 15. Open Design Questions (Tracked, Not Blocking)

- Optimal heuristics for obsolete test detection
- Cost-based task prioritization (tokens vs benefit)
- CI-native execution modes
- Multi-repo orchestration

These are intentionally deferred.
