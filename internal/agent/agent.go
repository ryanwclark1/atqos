package agent

import "context"

type Agent interface {
	Name() string
	Invoke(ctx context.Context, req Request) (Result, error)
}

type Request struct {
	SchemaVersion int               `json:"schema_version"`
	RunID         string            `json:"run_id"`
	TaskID        int64             `json:"task_id"`
	TaskType      string            `json:"task_type"`
	Tool          string            `json:"tool"`
	RepoPath      string            `json:"repo_path"`
	WorkspacePath string            `json:"workspace_path"`
	AllowedPaths  []string          `json:"allowed_paths"`
	ReadOnlyPaths []string          `json:"read_only_paths"`
	Targets       map[string]string `json:"targets,omitempty"`
	Instructions  string            `json:"instructions"`
	Validation    Validation        `json:"validation"`
}

type Validation struct {
	Commands []string `json:"commands"`
}

type Result struct {
	SchemaVersion int      `json:"schema_version"`
	RunID         string   `json:"run_id"`
	TaskID        int64    `json:"task_id"`
	Status        string   `json:"status"`
	Summary       string   `json:"summary"`
	FilesChanged  []string `json:"files_changed"`
}
