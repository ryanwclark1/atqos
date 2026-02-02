package runner

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type Command struct {
	Args           []string
	Env            map[string]string
	Cwd            string
	TimeoutSeconds int
	AllowNonZero   bool
	StdoutPath     string
	StderrPath     string
	CombinedPath   string
}

type ExecResult struct {
	ExitCode     int
	StartedAt    time.Time
	FinishedAt   time.Time
	StdoutPath   string
	StderrPath   string
	CombinedPath string
	DurationMs   int64
}

type Runner interface {
	Run(ctx context.Context, cmd Command) (ExecResult, error)
}

type GenericRunner struct {
	artifactRoot string
}

func NewGenericRunner(artifactRoot string) *GenericRunner {
	return &GenericRunner{artifactRoot: artifactRoot}
}

func (r *GenericRunner) Run(ctx context.Context, cmd Command) (ExecResult, error) {
	if len(cmd.Args) == 0 {
		return ExecResult{}, fmt.Errorf("command args required")
	}

	start := time.Now()
	ctx = applyTimeout(ctx, cmd.TimeoutSeconds)

	execCmd := exec.CommandContext(ctx, cmd.Args[0], cmd.Args[1:]...)
	if cmd.Cwd != "" {
		execCmd.Dir = cmd.Cwd
	}

	if len(cmd.Env) > 0 {
		execCmd.Env = append(os.Environ(), envSlice(cmd.Env)...)
	}

	stdoutPath := cmd.StdoutPath
	if stdoutPath == "" {
		stdoutPath = filepath.Join(r.artifactRoot, "stdout.log")
	}

	stderrPath := cmd.StderrPath
	if stderrPath == "" {
		stderrPath = filepath.Join(r.artifactRoot, "stderr.log")
	}

	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		return ExecResult{}, err
	}
	defer stdoutFile.Close()

	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		return ExecResult{}, err
	}
	defer stderrFile.Close()

	var stdout io.Writer = stdoutFile
	var stderr io.Writer = stderrFile

	if cmd.CombinedPath != "" {
		combinedFile, err := os.Create(cmd.CombinedPath)
		if err != nil {
			return ExecResult{}, err
		}
		defer combinedFile.Close()
		stdout = io.MultiWriter(stdoutFile, combinedFile)
		stderr = io.MultiWriter(stderrFile, combinedFile)
	}

	execCmd.Stdout = stdout
	execCmd.Stderr = stderr

	err = execCmd.Run()
	exitCode := exitCode(err)
	if err != nil && !cmd.AllowNonZero && exitCode != 0 {
		return ExecResult{}, fmt.Errorf("command failed: %w", err)
	}

	finished := time.Now()
	return ExecResult{
		ExitCode:     exitCode,
		StartedAt:    start,
		FinishedAt:   finished,
		StdoutPath:   stdoutPath,
		StderrPath:   stderrPath,
		CombinedPath: cmd.CombinedPath,
		DurationMs:   finished.Sub(start).Milliseconds(),
	}, nil
}

type Registry struct {
	runners map[string]Runner
}

func NewRegistry(artifactRoot string) *Registry {
	generic := NewGenericRunner(artifactRoot)
	return &Registry{
		runners: map[string]Runner{
			"generic": generic,
			"python":  generic,
		},
	}
}

func (r *Registry) Get(name string) Runner {
	if runner, ok := r.runners[name]; ok {
		return runner
	}
	return r.runners["generic"]
}

func applyTimeout(ctx context.Context, seconds int) context.Context {
	if seconds <= 0 {
		return ctx
	}
	timeout := time.Duration(seconds) * time.Second
	ctx, _ = context.WithTimeout(ctx, timeout)
	return ctx
}

func envSlice(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for key, value := range env {
		out = append(out, fmt.Sprintf("%s=%s", key, value))
	}
	return out
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return 1
}
