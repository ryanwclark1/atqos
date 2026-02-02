package core

import (
	"context"

	"atqos/internal/config"
	"atqos/internal/repo"
	"atqos/internal/runner"
)

type RunContext struct {
	RunID          string
	RepoPath       string
	ArtifactRoot   string
	RunnerRegistry *runner.Registry
	EventLog       EventLogger
	Config         config.Config
	RepoAdapter    *repo.Adapter
}

type Event struct {
	RunID     string      `json:"run_id"`
	Level     string      `json:"level"`
	EventType string      `json:"event_type"`
	Tool      string      `json:"tool,omitempty"`
	TaskID    int64       `json:"task_id,omitempty"`
	AttemptID int64       `json:"attempt_id,omitempty"`
	Payload   interface{} `json:"payload,omitempty"`
}

type EventLogger interface {
	Emit(event Event) error
}

type Plugin interface {
	ID() string
	Collect(ctx context.Context, rc RunContext) (ArtifactSet, error)
	Normalize(ctx context.Context, rc RunContext, artifacts ArtifactSet) ([]FindingRecord, error)
	Plan(ctx context.Context, rc RunContext, findings []FindingRecord) ([]TaskRecord, error)
	ValidationSpec(ctx context.Context, rc RunContext, task TaskRecord) (ValidationSpec, error)
}
