package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type CodexCLIAdapter struct {
	command []string
}

func NewCodexCLI(command []string) *CodexCLIAdapter {
	return &CodexCLIAdapter{command: command}
}

func (a *CodexCLIAdapter) Name() string {
	return "codex"
}

func (a *CodexCLIAdapter) Invoke(ctx context.Context, req Request) (Result, error) {
	command := a.command
	if len(command) == 0 {
		command = defaultCodexCommand()
	}
	if len(command) == 0 {
		return Result{}, fmt.Errorf("codex command not configured")
	}

	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	stdin := &bytes.Buffer{}
	if err := json.NewEncoder(stdin).Encode(req); err != nil {
		return Result{}, err
	}
	cmd.Stdin = stdin

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return Result{}, fmt.Errorf("codex command failed: %w", err)
	}

	var result Result
	if err := json.NewDecoder(&stdout).Decode(&result); err != nil {
		return Result{}, fmt.Errorf("decode codex result: %w", err)
	}
	if result.SchemaVersion != req.SchemaVersion {
		return Result{}, fmt.Errorf("codex result schema mismatch: %d", result.SchemaVersion)
	}
	if result.RunID != req.RunID || result.TaskID != req.TaskID {
		return Result{}, fmt.Errorf("codex result does not match request")
	}

	return result, nil
}

func defaultCodexCommand() []string {
	command := os.Getenv("ATQOS_CODEX_CMD")
	if command == "" {
		return []string{"codex"}
	}
	return strings.Fields(command)
}
