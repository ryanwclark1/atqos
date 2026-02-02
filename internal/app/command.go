package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"atqos/internal/config"
	"atqos/internal/core"
	"atqos/internal/eventlog"
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
			return finalize(storeDB, runID, summary, err)
		}

		for _, artifact := range artifacts.Items {
			if err := storeDB.AddArtifact(ctx, artifact); err != nil {
				return finalize(storeDB, runID, summary, err)
			}
		}

		findings, err := plugin.Normalize(ctx, runCtx, artifacts)
		if err != nil {
			return finalize(storeDB, runID, summary, err)
		}

		if err := storeDB.InsertFindings(ctx, findings); err != nil {
			return finalize(storeDB, runID, summary, err)
		}

		tasks, err := plugin.Plan(ctx, runCtx, findings)
		if err != nil {
			return finalize(storeDB, runID, summary, err)
		}

		for i := range tasks {
			spec, err := plugin.ValidationSpec(ctx, runCtx, tasks[i])
			if err != nil {
				return finalize(storeDB, runID, summary, err)
			}
			validationJSON, err := json.Marshal(spec)
			if err != nil {
				return finalize(storeDB, runID, summary, err)
			}
			tasks[i].ValidationJSON = string(validationJSON)
		}

		if err := storeDB.InsertTasks(ctx, tasks); err != nil {
			return finalize(storeDB, runID, summary, err)
		}

		summary.Add(findings, tasks)
	}

	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		return finalize(storeDB, runID, summary, err)
	}

	if err := storeDB.UpdateRunStatus(ctx, runID, core.RunStatusSucceeded, string(summaryJSON)); err != nil {
		return Result{}, err
	}

	return Result{
		RunID:   runID,
		Status:  core.RunStatusSucceeded,
		Summary: summary.String(),
	}, nil
}

func finalize(storeDB *store.SQLiteStore, runID string, summary core.Summary, runErr error) (Result, error) {
	summaryJSON, err := json.Marshal(summary)
	if err != nil {
		return Result{}, err
	}

	if updateErr := storeDB.UpdateRunStatus(context.Background(), runID, core.RunStatusFailed, string(summaryJSON)); updateErr != nil {
		return Result{}, updateErr
	}

	return Result{}, runErr
}
