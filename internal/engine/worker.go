package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"atqos/internal/agent"
	"atqos/internal/core"
	"atqos/internal/git"
	"atqos/internal/runner"
	"atqos/internal/store"
)

type Executor struct {
	Store       *store.SQLiteStore
	RunContext  core.RunContext
	Agent       agent.Agent
	GitStrategy git.Strategy
	Plugins     []core.Plugin
}

func (e *Executor) Run(ctx context.Context) error {
	workers := e.RunContext.Config.MaxWorkers
	if workers < 1 {
		workers = 1
	}

	var wg sync.WaitGroup
	checkpoint := newCheckpointTracker(e.RunContext.Config.CheckpointMins)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			e.runWorker(ctx, fmt.Sprintf("worker-%d", workerID), checkpoint)
		}(i + 1)
	}

	wg.Wait()
	return nil
}

func (e *Executor) runWorker(ctx context.Context, workerID string, checkpoint *checkpointTracker) {
	for {
		task, err := e.Store.ClaimNextTask(ctx, e.RunContext.RunID, workerID)
		if err != nil || task == nil {
			return
		}

		attempt := core.AttemptRecord{
			TaskID:    task.ID,
			AttemptNo: 1,
			Status:    "running",
			AgentName: e.Agent.Name(),
			StartedAt: time.Now(),
		}
		attemptID, err := e.Store.CreateAttempt(ctx, attempt)
		if err != nil {
			_ = e.Store.UpdateTaskStatus(ctx, task.ID, "blocked", `{"error":"failed to create attempt"}`)
			return
		}

		workspace, err := e.GitStrategy.PrepareWorkspace(ctx, e.RunContext.RepoPath, task.ID)
		if err != nil {
			_ = e.Store.UpdateTaskStatus(ctx, task.ID, "blocked", `{"error":"failed to prepare workspace"}`)
			_ = e.Store.FinishAttempt(ctx, attemptID, "failed", `{"error":"workspace failure"}`, 1)
			return
		}

		validationSpec, err := validationSpec(task.ValidationJSON)
		if err != nil {
			_ = e.Store.UpdateTaskStatus(ctx, task.ID, "blocked", `{"error":"invalid validation spec"}`)
			_ = e.Store.FinishAttempt(ctx, attemptID, "failed", `{"error":"validation spec failure"}`, 1)
			return
		}

		agentReq := agent.Request{
			SchemaVersion: 1,
			RunID:         e.RunContext.RunID,
			TaskID:        task.ID,
			TaskType:      task.TaskType,
			Tool:          task.Tool,
			RepoPath:      e.RunContext.RepoPath,
			WorkspacePath: workspace.Path,
			AllowedPaths:  e.RunContext.Config.AllowedPaths,
			ReadOnlyPaths: []string{".git", e.RunContext.ArtifactRoot},
			Instructions:  task.Description,
			Validation: agent.Validation{
				Commands: validationStrings(validationSpec),
			},
		}

		_, agentErr := e.Agent.Invoke(ctx, agentReq)

		validationExit := runValidation(ctx, e.RunContext, validationSpec, workspace.Path)
		status := "succeeded"
		if agentErr != nil || validationExit != 0 {
			status = "failed"
		}

		_ = e.Store.FinishAttempt(ctx, attemptID, status, "", validationExit)
		if status == "succeeded" {
			_ = e.Store.UpdateTaskStatus(ctx, task.ID, "succeeded", "")
		} else {
			_ = e.Store.UpdateTaskStatus(ctx, task.ID, "blocked", "")
		}

		_ = e.GitStrategy.FinalizeWorkspace(ctx, workspace)

		if checkpoint.ShouldRun() {
			_ = e.runCheckpoint(ctx)
		}
	}
}

func (e *Executor) runCheckpoint(ctx context.Context) error {
	if err := e.RunContext.EventLog.Emit(core.Event{
		RunID:     e.RunContext.RunID,
		Level:     "info",
		EventType: "checkpoint_started",
	}); err != nil {
		return err
	}

	for _, plugin := range e.Plugins {
		if !e.RunContext.Config.PluginEnabled(plugin.ID()) {
			continue
		}
		artifacts, err := plugin.Collect(ctx, e.RunContext)
		if err != nil {
			return err
		}
		for _, artifact := range artifacts.Items {
			if err := e.Store.AddArtifact(ctx, artifact); err != nil {
				return err
			}
		}

		findings, err := plugin.Normalize(ctx, e.RunContext, artifacts)
		if err != nil {
			return err
		}
		if err := e.Store.InsertFindings(ctx, findings); err != nil {
			return err
		}

		tasks, err := plugin.Plan(ctx, e.RunContext, findings)
		if err != nil {
			return err
		}
		for i := range tasks {
			spec, err := plugin.ValidationSpec(ctx, e.RunContext, tasks[i])
			if err != nil {
				return err
			}
			validationJSON, err := json.Marshal(spec)
			if err != nil {
				return err
			}
			tasks[i].ValidationJSON = string(validationJSON)
		}
		if err := e.Store.InsertTasks(ctx, tasks); err != nil {
			return err
		}
	}

	return e.RunContext.EventLog.Emit(core.Event{
		RunID:     e.RunContext.RunID,
		Level:     "info",
		EventType: "checkpoint_finished",
	})
}

func validationSpec(validationJSON string) (core.ValidationSpec, error) {
	var spec core.ValidationSpec
	if err := json.Unmarshal([]byte(validationJSON), &spec); err != nil {
		return core.ValidationSpec{}, err
	}
	return spec, nil
}

func validationStrings(spec core.ValidationSpec) []string {
	out := make([]string, 0, len(spec.Commands))
	for _, command := range spec.Commands {
		out = append(out, joinArgs(command.Args))
	}
	return out
}

func runValidation(ctx context.Context, runCtx core.RunContext, spec core.ValidationSpec, workspace string) int {
	exitCode := 0
	for _, command := range spec.Commands {
		cmd := runner.Command{
			Args:         command.Args,
			Cwd:          workspace,
			AllowNonZero: true,
		}
		result, err := runCtx.RunnerRegistry.Get(command.Runner).Run(ctx, cmd)
		if err != nil {
			exitCode = 1
		}
		if result.ExitCode != 0 {
			exitCode = result.ExitCode
		}
	}
	return exitCode
}

func joinArgs(args []string) string {
	return strings.Join(args, " ")
}

type checkpointTracker struct {
	interval time.Duration
	lastRun  time.Time
	mu       sync.Mutex
}

func newCheckpointTracker(minutes int) *checkpointTracker {
	if minutes <= 0 {
		minutes = 30
	}
	return &checkpointTracker{
		interval: time.Duration(minutes) * time.Minute,
		lastRun:  time.Now(),
	}
}

func (c *checkpointTracker) ShouldRun() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.lastRun) < c.interval {
		return false
	}
	c.lastRun = time.Now()
	return true
}
