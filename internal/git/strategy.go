package git

import "context"

type Strategy interface {
	PrepareWorkspace(ctx context.Context, repoPath string, taskID int64) (Workspace, error)
	FinalizeWorkspace(ctx context.Context, ws Workspace) error
}

type Workspace struct {
	Path     string
	Branch   string
	Worktree bool
}
