# ATQOS Technical Design v1

This document derives implementation-level specifications from the ATQOS scope. It defines:

- Go interfaces (core engine, runners, plugins, agents)
- SQLite schema (runs, findings, tasks, attempts, artifacts)
- Plugin interfaces and lifecycle
- Agent JSON contract (request/response payloads and validation)

It is designed so that v1 supports **pytest + coverage** while keeping the core extensible to **ruff/mypy/vitest** later without core rewrites.

---

## 0. Terminology

- **Run**: A single orchestrator execution against a repo snapshot.
- **Plugin**: A tool integration implementing Collect/Normalize/Plan/Validate.
- **Finding**: Normalized diagnostic produced by a plugin.
- **Task**: Work item derived from Findings, executed by a worker/agent.
- **Attempt**: One execution of a Task (agent invocation + validation).
- **Artifact**: File(s) produced during a Run (reports, logs, diffs, prompts).

---

## 1. High-Level Architecture (Executable Path)

### 1.1 Run Lifecycle

1. Initialize Run (create artifact dir, open SQLite, write run header event)
2. Baseline collection:

   - PytestPlugin.Collect() → raw pytest artifacts
   - PytestPlugin.Normalize() → Findings
   - PytestPlugin.Plan() → Tasks
3. Task scheduling:

   - insert Tasks into DB
   - start worker pool
4. For each Task:

   - checkout branch/worktree
   - invoke agent adapter (non-interactive)
   - run plugin-specific validation commands
   - persist Attempt results
5. Checkpoints:

   - periodic full re-Collect() to refresh baseline and re-plan if needed
6. Finalization:

   - final Collect
   - compute summary, write report, close Run

### 1.2 Extensibility Principles

- Core should not assume Python.
- Plugins choose their runner (python/node/generic).
- Normalized models are the contract between plugins and core.

---

## 2. Go Interfaces

### 2.1 Core Models (Structs)

#### RunContext

- Immutable-ish inputs + shared utilities.

Fields (conceptual):

- RunID string
- RepoPath string
- ArtifactRoot string
- WorkspaceRoot string (optional; for worktrees)
- GitStrategy GitStrategy
- RunnerRegistry RunnerRegistry
- Store Store
- EventLog EventLog
- Config Config

#### Finding

Required fields:

- ID (generated at insert time)
- Tool string (e.g., "pytest", "coverage")
- Kind string ("test_failure", "coverage_gap", ...)
- Severity string ("blocker", "high", "medium", "low")
- Fingerprint string (stable hash)
- Message string
- FilePath string (nullable)
- Line int (nullable)
- Column int (nullable)
- Symbol string (nullable)
- TestID string (nullable; e.g., pytest nodeid)
- RawRef string (artifact pointer or JSON pointer)
- MetaJSON string (optional JSON blob)

#### Task

Required fields:

- ID
- RunID
- Tool
- TaskType ("fix", "add_test", "remove_skip", "quarantine", "suppress")
- Priority int
- Status ("queued", "running", "succeeded", "blocked", "abandoned")
- Fingerprint (stable; derived from Finding(s))
- Title (short)
- Description (long)
- TargetsJSON (paths/nodeids)
- ValidationJSON (commands)
- RetryPolicyJSON
- DependsOnJSON

#### Attempt

- One execution of a Task.

Fields:

- ID
- TaskID
- AttemptNo
- Status ("running", "succeeded", "failed")
- AgentName
- AgentExitCode
- ValidationExitCode
- StartedAt, FinishedAt
- SummaryJSON
- DiffStatsJSON
- ArtifactsJSON

---

### 2.2 Event Logging

#### EventLog

Append-only JSONL writer.

Methods:

- Emit(ctx, Event) error

Event schema (minimum):

- ts (RFC3339)
- run_id
- level (info/warn/error)
- event_type (run_started, task_queued, attempt_finished, checkpoint, ...)
- task_id (optional)
- attempt_id (optional)
- tool (optional)
- payload (object)

---

### 2.3 Store Interface (SQLite)

#### Store

Methods:

- Init(ctx) error

- CreateRun(ctx, run *RunRecord) error

- UpdateRunStatus(ctx, runID string, status string, summaryJSON string) error

- InsertFinding(ctx, finding *FindingRecord) (id int64, err error)

- InsertFindings(ctx, findings []FindingRecord) error

- InsertTask(ctx, task *TaskRecord) (id int64, err error)

- InsertTasks(ctx, tasks []TaskRecord) error

- ClaimNextTask(ctx, runID string, workerID string) (*TaskRecord, error)

- UpdateTaskStatus(ctx, taskID int64, status string, extraJSON string) error

- CreateAttempt(ctx, attempt *AttemptRecord) (id int64, err error)

- FinishAttempt(ctx, attemptID int64, status string, summaryJSON string) error

- AddArtifact(ctx, artifact *ArtifactRecord) (id int64, err error)

- GetRunSummary(ctx, runID string) (*RunSummary, error)

Implementation notes:

- Use WAL mode
- Use transactions for claim/update
- ClaimNextTask must be atomic (SELECT ... FOR UPDATE equivalent pattern)

---

### 2.4 Runner Abstraction

Runners execute external commands and capture outputs.

#### Command

- Args []string
- Env map[string]string
- Cwd string
- TimeoutSeconds int
- AllowNonZero bool

#### ExecResult

- ExitCode int
- StartedAt, FinishedAt
- StdoutPath string
- StderrPath string
- CombinedPath string (optional)
- DurationMs int64

#### Runner

Methods:

- Run(ctx, cmd Command) (ExecResult, error)

#### RunnerRegistry

Methods:

- Get(name string) Runner

Initial runners:

- GenericRunner (exec.Cmd)
- PythonRunner (wraps GenericRunner but resolves python invocation)

PythonRunner policy:

- Resolves interpreter according to RepoAdapter (see §4)
- Always executes using explicit interpreter / tool command (no imports)

---

### 2.5 Git Strategy

#### GitStrategy

Methods:

- PrepareWorkspace(ctx, task TaskRecord) (Workspace, error)
- FinalizeWorkspace(ctx, ws Workspace, task TaskRecord, attempt AttemptRecord) error

#### Workspace

Fields:

- Path string
- Branch string
- Worktree bool

Recommended v1 implementation:

- Per-task worktree OR per-task branch in a separate clone

---

### 2.6 Agent Interface

#### Agent

Methods:

- Name() string
- Invoke(ctx, req AgentRequest) (AgentResult, error)

Implementations (v1):

- CodexCLIAdapter
- ClaudeCodeAdapter
- CursorAdapter

---

## 3. Plugin Interfaces

### 3.1 Plugin Lifecycle

All plugins implement:

1. Collect → produces raw artifacts
2. Normalize → produces Findings
3. Plan → produces Tasks
4. Validate → validates a Task (or provides validation commands)

### 3.2 Go Plugin Interface

#### Plugin

Methods:

- ID() string
- Collect(ctx, rc RunContext) (ArtifactSet, error)
- Normalize(ctx, rc RunContext, artifacts ArtifactSet) ([]FindingRecord, error)
- Plan(ctx, rc RunContext, findings []FindingRecord, baseline BaselineState) ([]TaskRecord, error)
- ValidationSpec(ctx, rc RunContext, task TaskRecord) (ValidationSpec, error)

Where:

#### ArtifactSet

- PluginID string
- Items []ArtifactRecord (or paths)

#### BaselineState

- SummaryJSON (tool-specific)
- PriorFindings []FindingRecord (optional)
- PriorTasks []TaskRecord (optional)

#### ValidationSpec

- Commands []CommandSpec
- SuccessCriteria SuccessCriteria

#### CommandSpec

- Runner string ("python", "node", "generic")
- Args []string
- Env map[string]string
- Cwd string
- TimeoutSeconds int

#### SuccessCriteria

- RequireExitCode0 bool
- MaxNewFindings int (optional)
- MinCoverageDelta float64 (optional)

---

### 3.3 Pytest Plugin (v1)

#### Collect

Runs pytest with deterministic outputs:

- JSON report (pytest-json-report)
- JUnit XML
- Text log capture

Command template (conceptual):

- python -m pytest --json-report --json-report-file=... --junitxml=... -ra --showlocals

Artifacts:

- pytest/report.json
- pytest/junit.xml
- pytest/log.txt

#### Normalize

Reads JSON report and extracts:

- failures/errors
- nodeid/test id
- traceback/longrepr
- captured output

Produces Findings:

- tool: pytest
- kind: test_failure
- severity: blocker (for import/collection errors), else high/medium
- fingerprint: hash(nodeid + exception type + primary stack frame)

#### Plan

Creates tasks using heuristics:

- default chunk: one test file per task
- systemic bucket: if N files share same exception signature

Task types:

- fix (default)
- refactor (systemic)
- quarantine (when retry cap reached)

#### ValidationSpec

Default validation for a fix task:

- targeted pytest run for the affected file(s) or nodeids
- optional: run with -q to keep logs small

Checkpoints:

- full suite rerun every N successes OR T minutes

---

### 3.4 Coverage Plugin (v1)

Option A: integrated with pytest plugin (same run)
Option B: separate plugin.

Collect:

- python -m pytest --cov=. --cov-report=json:... --cov-report=term

Normalize:

- parse coverage.json
- create Findings:

  - kind: coverage_gap
  - file path + percent covered

Plan:

- create add_test tasks per source module

Validation:

- targeted tests for that module + re-run coverage json
- success: coverage delta >= threshold (configurable)

---

## 4. Repo Adapter (Environment Discovery)

Core must not be Python-specific, but plugins need a way to run toolchains.

### 4.1 RepoAdapter Interface

Methods:

- Detect(ctx, repoPath string) (RepoProfile, error)
- ResolvePython(ctx, profile RepoProfile) (PythonInvocation, error)
- EnsureDeps(ctx, profile RepoProfile) (EnsureResult, error)  // optional v1

### 4.2 RepoProfile

Fields:

- RepoPath
- PythonManager ("uv", "poetry", "venv", "system")
- HasUVLock bool
- HasPoetryLock bool
- VenvPath string (nullable)

### 4.3 PythonInvocation

Two supported invocation modes:

**Interpreter mode** (preferred):

- PythonPath: /path/to/.venv/bin/python
- PrefixArgs: []string{} (none)

**Tool-runner mode** (when no venv):

- Tool: "uv"
- PrefixArgs: ["run", "python"]
- Final command becomes: uv run python -m pytest ...

Policy:

- Always execute tools via explicit invocation
- Never import target code into orchestrator

---

## 5. Agent JSON Contract

Agents must be invokable non-interactively and return a machine-parseable JSON summary.

### 5.1 AgentRequest (input to adapter)

Fields:

- schema_version: 1
- run_id
- task_id
- task_type
- tool
- repo_path
- workspace_path
- allowed_paths: ["tests/", "src/"] (configurable)
- read_only_paths: [".git/", "artifacts/"]
- targets: object

  - files: []
  - test_ids: []
  - modules: []
- context:

  - baseline_summary_ref (artifact pointer)
  - findings_ref(s)
  - previous_attempt_ref(s)
- instructions:

  - system_prompt (optional)
  - task_prompt (required)
- validation:

  - commands: []CommandSpec (exact commands to run)
  - success_criteria
- constraints:

  - max_files_changed
  - max_lines_changed
  - forbid_delete_paths

Adapters may translate this into the native CLI format of the agent.

### 5.2 AgentResult (JSON printed to stdout)

Agents must output **exactly one JSON object** as their final stdout payload.

Fields:

- schema_version: 1
- run_id
- task_id
- status: "success" | "failure" | "blocked"
- summary: string (short)
- changes:

  - files_changed: []string
  - files_created: []string
  - files_deleted: []string
  - diff_stat:

    - insertions: int
    - deletions: int
- actions:

  - commands_ran: []string
  - tests_ran: []string
- validation:

  - validation_exit_code: int
  - validation_summary: object (tool specific; e.g., pytest counts)
- notes:

  - systematic_issue: bool
  - suggested_followups: []string
- artifacts:

  - prompt_ref: string
  - stdout_ref: string
  - stderr_ref: string
  - patch_ref: string

### 5.3 Enforcement Rules

Core must treat agent output as advisory and enforce:

- Validate exit codes against SuccessCriteria
- Reject if changes touch disallowed paths
- Reject if diff exceeds limits
- Reject if agent claims success but validation failed

If rejected:

- attempt marked failed
- new attempt created (bounded by retry policy)

---

## 6. SQLite Schema (v1)

### 6.1 Design Notes

- Use SQLite WAL mode
- Prefer integer primary keys + stable external ids (run_id string)
- Store rich data as JSON TEXT in columns where appropriate
- Ensure atomic task claim

### 6.2 DDL

```sql
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

-- Runs
CREATE TABLE IF NOT EXISTS runs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id TEXT NOT NULL UNIQUE,
  repo_path TEXT NOT NULL,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  status TEXT NOT NULL, -- running|succeeded|failed|aborted
  config_json TEXT NOT NULL,
  summary_json TEXT
);

-- Artifacts
CREATE TABLE IF NOT EXISTS artifacts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id TEXT NOT NULL,
  tool TEXT,
  kind TEXT NOT NULL, -- report|log|diff|prompt|stdout|stderr|json
  path TEXT NOT NULL,
  sha256 TEXT,
  size_bytes INTEGER,
  created_at TEXT NOT NULL,
  meta_json TEXT,
  FOREIGN KEY(run_id) REFERENCES runs(run_id)
);

CREATE INDEX IF NOT EXISTS idx_artifacts_run_id ON artifacts(run_id);

-- Findings
CREATE TABLE IF NOT EXISTS findings (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id TEXT NOT NULL,
  tool TEXT NOT NULL,
  kind TEXT NOT NULL,
  severity TEXT NOT NULL,
  fingerprint TEXT NOT NULL,
  message TEXT NOT NULL,
  file_path TEXT,
  line INTEGER,
  col INTEGER,
  symbol TEXT,
  test_id TEXT,
  raw_ref TEXT,
  meta_json TEXT,
  created_at TEXT NOT NULL,
  FOREIGN KEY(run_id) REFERENCES runs(run_id)
);

CREATE INDEX IF NOT EXISTS idx_findings_run_tool ON findings(run_id, tool);
CREATE INDEX IF NOT EXISTS idx_findings_fingerprint ON findings(fingerprint);

-- Tasks
CREATE TABLE IF NOT EXISTS tasks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id TEXT NOT NULL,
  tool TEXT NOT NULL,
  task_type TEXT NOT NULL,
  priority INTEGER NOT NULL,
  status TEXT NOT NULL, -- queued|running|succeeded|blocked|abandoned
  fingerprint TEXT NOT NULL,
  title TEXT NOT NULL,
  description TEXT,
  targets_json TEXT NOT NULL,
  validation_json TEXT NOT NULL,
  retry_policy_json TEXT NOT NULL,
  depends_on_json TEXT,
  claimed_by TEXT,
  claimed_at TEXT,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  FOREIGN KEY(run_id) REFERENCES runs(run_id)
);

CREATE INDEX IF NOT EXISTS idx_tasks_run_status ON tasks(run_id, status);
CREATE INDEX IF NOT EXISTS idx_tasks_fingerprint ON tasks(fingerprint);

-- Attempts
CREATE TABLE IF NOT EXISTS attempts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id INTEGER NOT NULL,
  attempt_no INTEGER NOT NULL,
  status TEXT NOT NULL, -- running|succeeded|failed
  agent_name TEXT,
  agent_exit_code INTEGER,
  validation_exit_code INTEGER,
  started_at TEXT NOT NULL,
  finished_at TEXT,
  summary_json TEXT,
  diff_stats_json TEXT,
  artifacts_json TEXT,
  FOREIGN KEY(task_id) REFERENCES tasks(id)
);

CREATE INDEX IF NOT EXISTS idx_attempts_task_id ON attempts(task_id);

-- Task ↔ Finding mapping (optional but useful)
CREATE TABLE IF NOT EXISTS task_findings (
  task_id INTEGER NOT NULL,
  finding_id INTEGER NOT NULL,
  PRIMARY KEY(task_id, finding_id),
  FOREIGN KEY(task_id) REFERENCES tasks(id),
  FOREIGN KEY(finding_id) REFERENCES findings(id)
);
```

### 6.3 Atomic Task Claim Pattern

SQLite lacks SELECT FOR UPDATE; implement claim as:

1. Begin IMMEDIATE transaction
2. Select next task with status='queued' ordered by priority
3. Update that row setting status='running', claimed_by, claimed_at
4. Commit

This ensures only one worker claims each task.

---

## 7. Configuration (v1)

Store configuration in:

- `config_json` in runs table
- optionally `atqos.yaml` in repo root (not required)

Key config fields:

- max_workers
- max_agent_workers
- retry caps
- checkpoint frequency (N tasks / minutes)
- allowed paths
- git strategy
- tool commands overrides

---

## 8. Compatibility & Future Plugins

This design allows adding:

- Ruff plugin (Collect: ruff JSON; Plan: fixable vs non-fixable; Validate: ruff check)
- Mypy/Pyright plugin (Normalize file/line/code; Validate per file + full)
- Vitest plugin (Collect: vitest json; Plan tasks by test file; Validate targeted)

Core remains unchanged.

---

## 9. v1 Deliverables Checklist

- [ ] Core runner + event log + artifacts
- [ ] SQLite store implementation
- [ ] Worker pool + atomic claim
- [ ] GitStrategy (per-task branch/worktree)
- [ ] Agent adapter for one CLI
- [ ] Pytest plugin (Collect/Normalize/Plan/ValidationSpec)
- [ ] Coverage plugin or integrated coverage
- [ ] Checkpoint rerun
- [ ] Final run summary report
