package git

import "context"

type InPlaceStrategy struct{}

func NewInPlace() *InPlaceStrategy {
	return &InPlaceStrategy{}
}

func (s *InPlaceStrategy) PrepareWorkspace(ctx context.Context, repoPath string, taskID int64) (Workspace, error) {
	return Workspace{Path: repoPath, Worktree: false}, nil
}

func (s *InPlaceStrategy) FinalizeWorkspace(ctx context.Context, ws Workspace) error {
	return nil
}
