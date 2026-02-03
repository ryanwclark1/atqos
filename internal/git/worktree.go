package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

type WorktreeStrategy struct {
	Root string
}

func NewWorktree(root string) *WorktreeStrategy {
	return &WorktreeStrategy{Root: root}
}

func (s *WorktreeStrategy) PrepareWorkspace(ctx context.Context, repoPath string, taskID int64) (Workspace, error) {
	if err := os.MkdirAll(s.Root, 0o755); err != nil {
		return Workspace{}, err
	}
	branch := fmt.Sprintf("atqos/task-%d", taskID)
	path := filepath.Join(s.Root, fmt.Sprintf("task-%d", taskID))

	cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "worktree", "add", "-b", branch, path)
	if err := cmd.Run(); err != nil {
		return Workspace{}, fmt.Errorf("create worktree: %w", err)
	}
	return Workspace{Path: path, Branch: branch, Worktree: true}, nil
}

func (s *WorktreeStrategy) FinalizeWorkspace(ctx context.Context, ws Workspace) error {
	if !ws.Worktree {
		return nil
	}
	cmd := exec.CommandContext(ctx, "git", "-C", ws.Path, "worktree", "remove", "--force", ws.Path)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("remove worktree: %w", err)
	}
	return nil
}
