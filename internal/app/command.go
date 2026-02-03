package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"atqos/internal/agent"
	"atqos/internal/config"
	"atqos/internal/core"
	"atqos/internal/engine"
	"atqos/internal/eventlog"
	"atqos/internal/git"
	"atqos/internal/plugins/coverage"
	"atqos/internal/plugins/pytest"
	"atqos/internal/repo"
	"atqos/internal/runner"
	"atqos/internal/store"
)

type Command struct {
	RepoPath    string
	ArtifactDir string
	DBPath      string
	ConfigPath  string
}

type Result struct {
	RunID   string
	Status  string
	Summary string
}

func (c Command) Run(ctx context.Context) (Result, error) {
	cfg, err := config.Load(c.ConfigPath)
	if err != nil {
		return Result{}, err
	}

	runID, err := core.NewRunID()
	if err != nil {
		return Result{}, err
	}

	artifactRoot := filepath.Join(c.ArtifactDir, runID)
	if err := os.MkdirAll(artifactRoot, 0o755); err != nil {
		return Result{}, fmt.Errorf("create artifact root: %w", err)
	}

	logPath := filepath.Join(artifactRoot, "events.jsonl")
	logger, err := eventlog.New(logPath)
	if err != nil {
		return Result{}, err
	}
	defer logger.Close()

	storeDB, err := store.NewSQLite(c.DBPath)
	if err != nil {
		return Result{}, err
	}
	defer storeDB.Close()

	if err := storeDB.Init(ctx); err != nil {
		return Result{}, err
	}

	configJSON, err := json.Marshal(cfg)
	if err != nil {
		return Result{}, fmt.Errorf("marshal config: %w", err)
	}

	run := core.RunRecord{
		RunID:     runID,
		RepoPath:  c.RepoPath,
		StartedAt: time.Now(),
		Status:    core.RunStatusRunning,
		Config:    string(configJSON),
	}
	if err := storeDB.CreateRun(ctx, run); err != nil {
		return Result{}, err
	}
	if err := logger.Emit(core.Event{
		RunID:     runID,
		Level:     "info",
		EventType: "run_started",
		Payload: map[string]string{
			"repo_path": c.RepoPath,
		},
	}); err != nil {
		return Result{}, err
	}

	runnerRegistry := runner.NewRegistry(artifactRoot)
	adapter := repo.NewAdapter()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	runCtx := core.RunContext{
		RunID:          runID,
		RepoPath:       c.RepoPath,
		ArtifactRoot:   artifactRoot,
		RunnerRegistry: runnerRegistry,
		EventLog:       logger,
		Config:         cfg,
		RepoAdapter:    adapter,
	}

	plugins := []core.Plugin{
		pytest.New(),
		coverage.New(),
	}

	summary := core.Summary{}
	for _, plugin := range plugins {
		if !cfg.PluginEnabled(plugin.ID()) {
			continue
		}

		if err := logger.Emit(core.Event{
			RunID:     runID,
			Level:     "info",
			EventType: "collect_started",
			Tool:      plugin.ID(),
		}); err != nil {
			return Result{}, err
		}

		artifacts, err := plugin.Collect(ctx, runCtx)
		if err != nil {
			return finalize(storeDB, logger, runID, summary, err)
		}
		if err := logger.Emit(core.Event{
			RunID:     runID,
			Level:     "info",
			EventType: "collect_finished",
			Tool:      plugin.ID(),
			Payload: map[string]int{
				"artifact_count": len(artifacts.Items),
			},
		}); err != nil {
			return Result{}, err
		}

		for _, artifact := range artifacts.Items {
			if err := storeDB.AddArtifact(ctx, artifact); err != nil {
				return finalize(storeDB, logger, runID, summary, err)
			}
		}

		findings, err := plugin.Normalize(ctx, runCtx, artifacts)
		if err != nil {
			return finalize(storeDB, logger, runID, summary, err)
		}
		if err := logger.Emit(core.Event{
			RunID:     runID,
			Level:     "info",
			EventType: "normalize_finished",
			Tool:      plugin.ID(),
			Payload: map[string]int{
				"finding_count": len(findings),
			},
		}); err != nil {
			return Result{}, err
		}

		if err := storeDB.InsertFindings(ctx, findings); err != nil {
			return finalize(storeDB, logger, runID, summary, err)
		}

		tasks, err := plugin.Plan(ctx, runCtx, findings)
		if err != nil {
			return finalize(storeDB, logger, runID, summary, err)
		}
		if err := logger.Emit(core.Event{
			RunID:     runID,
			Level:     "info",
			EventType: "plan_finished",
			Tool:      plugin.ID(),
			Payload: map[string]int{
				"task_count": len(tasks),
			},
		}); err != nil {
			return Result{}, err
		}

		for i := range tasks {
			spec, err := plugin.ValidationSpec(ctx, runCtx, tasks[i])
			if err != nil {
				return finalize(storeDB, logger, runID, summary, err)
			}
			validationJSON, err := json.Marshal(spec)
			if err != nil {
				return finalize(storeDB, logger, runID, summary, err)
			}
			tasks[i].ValidationJSON = string(validationJSON)
		}

		if err := storeDB.InsertTasks(ctx, tasks); err != nil {
			return finalize(storeDB, logger, runID, summary, err)
		}

		summary.Add(findings, tasks)
	}

	agentAdapter := selectAgentAdapter()
	gitStrategy := selectGitStrategy(cfg.GitStrategy, artifactRoot)
	executor := engine.Executor{
		Store:       storeDB,
		RunContext:  runCtx,
		Agent:       agentAdapter,
		GitStrategy: gitStrategy,
		Plugins:     plugins,
	}
	if err := executor.Run(ctx); err != nil {
		return finalize(storeDB, logger, runID, summary, err)
	}

	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		return finalize(storeDB, logger, runID, summary, err)
	}

	if err := storeDB.UpdateRunStatus(ctx, runID, core.RunStatusSucceeded, string(summaryJSON)); err != nil {
		return Result{}, err
	}

	report, err := buildRunReport(ctx, storeDB, runID, summary)
	if err != nil {
		return Result{}, err
	}
	reportPath := filepath.Join(artifactRoot, "summary.json")
	if err := writeJSON(reportPath, report); err != nil {
		return Result{}, err
	}
	if err := storeDB.AddArtifact(ctx, newArtifact(runID, "core", "summary", reportPath)); err != nil {
		return Result{}, err
	}

	if err := logger.Emit(core.Event{
		RunID:     runID,
		Level:     "info",
		EventType: "run_finished",
		Payload: map[string]string{
			"status": core.RunStatusSucceeded,
		},
	}); err != nil {
		return Result{}, err
	}

	return Result{
		RunID:   runID,
		Status:  core.RunStatusSucceeded,
		Summary: summary.String(),
	}, nil
}

func finalize(storeDB *store.SQLiteStore, logger core.EventLogger, runID string, summary core.Summary, runErr error) (Result, error) {
	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		return Result{}, err
	}

	if updateErr := storeDB.UpdateRunStatus(context.Background(), runID, core.RunStatusFailed, string(summaryJSON)); updateErr != nil {
		return Result{}, updateErr
	}
	if logger != nil {
		_ = logger.Emit(core.Event{
			RunID:     runID,
			Level:     "error",
			EventType: "run_failed",
			Payload: map[string]string{
				"error": runErr.Error(),
			},
		})
	}

	return Result{}, runErr
}

type runReport struct {
	RunID     string       `json:"run_id"`
	Status    string       `json:"status"`
	Findings  int          `json:"findings"`
	Tasks     int          `json:"tasks"`
	StartedAt string       `json:"started_at"`
	Finished  string       `json:"finished_at"`
	Summary   core.Summary `json:"summary"`
}

func buildRunReport(ctx context.Context, storeDB *store.SQLiteStore, runID string, summary core.Summary) (runReport, error) {
	runSummary, err := storeDB.GetRunSummary(ctx, runID)
	if err != nil {
		return runReport{}, err
	}
	finished := ""
	if !runSummary.Finished.IsZero() {
		finished = runSummary.Finished.UTC().Format(time.RFC3339)
	}
	return runReport{
		RunID:     runSummary.RunID,
		Status:    runSummary.Status,
		Findings:  runSummary.Findings,
		Tasks:     runSummary.Tasks,
		StartedAt: runSummary.Started.UTC().Format(time.RFC3339),
		Finished:  finished,
		Summary:   summary,
	}, nil
}

func writeJSON(path string, payload interface{}) error {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func newArtifact(runID string, tool string, kind string, path string) core.ArtifactRecord {
	info, _ := os.Stat(path)
	size := int64(0)
	if info != nil {
		size = info.Size()
	}
	return core.ArtifactRecord{
		RunID:     runID,
		Tool:      tool,
		Kind:      kind,
		Path:      path,
		SizeBytes: size,
		CreatedAt: time.Now(),
	}
}

func selectGitStrategy(strategy string, artifactRoot string) git.Strategy {
	switch strategy {
	case "worktree":
		return git.NewWorktree(filepath.Join(artifactRoot, "worktrees"))
	default:
		return git.NewInPlace()
	}
}

func selectAgentAdapter() agent.Agent {
	codexCommand := strings.Fields(os.Getenv("ATQOS_CODEX_CMD"))
	if len(codexCommand) > 0 {
		return agent.NewCodexCLI(codexCommand)
	}
	command := strings.Fields(os.Getenv("ATQOS_AGENT_CMD"))
	if len(command) == 0 {
		return agent.NewLocal()
	}
	return agent.NewCommandAdapter("cli", command)
}
