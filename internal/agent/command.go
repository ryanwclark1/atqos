package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
)

type CommandAdapter struct {
	name    string
	command []string
}

func NewCommandAdapter(name string, command []string) *CommandAdapter {
	return &CommandAdapter{name: name, command: command}
}

func (a *CommandAdapter) Name() string {
	return a.name
}

func (a *CommandAdapter) Invoke(ctx context.Context, req Request) (Result, error) {
	if len(a.command) == 0 {
		return Result{}, fmt.Errorf("agent command not configured")
	}

	cmd := exec.CommandContext(ctx, a.command[0], a.command[1:]...)
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
		return Result{}, fmt.Errorf("agent command failed: %w", err)
	}

	var result Result
	if err := json.NewDecoder(&stdout).Decode(&result); err != nil {
		return Result{}, fmt.Errorf("decode agent result: %w", err)
	}

	return result, nil
}
