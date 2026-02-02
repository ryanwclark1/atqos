package coverage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"atqos/internal/core"
	"atqos/internal/runner"
)

type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) ID() string {
	return "coverage"
}

func (p *Plugin) Collect(ctx context.Context, rc core.RunContext) (core.ArtifactSet, error) {
	profile, err := rc.RepoAdapter.Detect(rc.RepoPath)
	if err != nil {
		return core.ArtifactSet{}, err
	}
	invocation, err := rc.RepoAdapter.ResolvePython(profile)
	if err != nil {
		return core.ArtifactSet{}, err
	}

	outputDir := filepath.Join(rc.ArtifactRoot, "coverage")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return core.ArtifactSet{}, err
	}

	reportPath := filepath.Join(outputDir, "coverage.json")
	stdoutPath := filepath.Join(outputDir, "stdout.log")
	stderrPath := filepath.Join(outputDir, "stderr.log")

	args := invocation.Command(
		"-m", "pytest",
		"--cov=.",
		"--cov-report=json:"+reportPath,
		"--cov-report=term",
	)

	cmd := runner.Command{
		Args:         args,
		Cwd:          rc.RepoPath,
		AllowNonZero: true,
		StdoutPath:   stdoutPath,
		StderrPath:   stderrPath,
	}

	_, err = rc.RunnerRegistry.Get("python").Run(ctx, cmd)
	if err != nil {
		return core.ArtifactSet{}, err
	}

	artifacts := []core.ArtifactRecord{
		newArtifact(rc.RunID, p.ID(), "report", reportPath),
		newArtifact(rc.RunID, p.ID(), "stdout", stdoutPath),
		newArtifact(rc.RunID, p.ID(), "stderr", stderrPath),
	}

	return core.ArtifactSet{PluginID: p.ID(), Items: artifacts}, nil
}

func (p *Plugin) Normalize(ctx context.Context, rc core.RunContext, artifacts core.ArtifactSet) ([]core.FindingRecord, error) {
	reportPath := ""
	for _, artifact := range artifacts.Items {
		if artifact.Kind == "report" {
			reportPath = artifact.Path
			break
		}
	}

	if reportPath == "" {
		return nil, fmt.Errorf("coverage report artifact missing")
	}

	data, err := os.ReadFile(reportPath)
	if err != nil {
		return nil, err
	}

	var report coverageReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, err
	}

	now := time.Now()
	findings := make([]core.FindingRecord, 0)
	for file, details := range report.Files {
		percent := details.Summary.PercentCovered
		if percent >= rc.Config.Coverage.MinimumThreshold*100 {
			continue
		}

		message := fmt.Sprintf("Coverage %.1f%% below threshold %.1f%%", percent, rc.Config.Coverage.MinimumThreshold*100)
		findings = append(findings, core.FindingRecord{
			RunID:       rc.RunID,
			Tool:        p.ID(),
			Kind:        "coverage_gap",
			Severity:    "medium",
			Fingerprint: hashCoverage(file, percent),
			Message:     message,
			FilePath:    file,
			RawRef:      reportPath,
			CreatedAt:   now,
		})
	}

	return findings, nil
}

func (p *Plugin) Plan(ctx context.Context, rc core.RunContext, findings []core.FindingRecord) ([]core.TaskRecord, error) {
	if len(findings) == 0 {
		return nil, nil
	}

	files := make([]string, 0, len(findings))
	for _, finding := range findings {
		if finding.FilePath == "" {
			continue
		}
		files = append(files, finding.FilePath)
	}
	sort.Strings(files)

	now := time.Now()
	tasks := make([]core.TaskRecord, 0, len(files))
	for _, file := range files {
		targets := map[string]interface{}{
			"files": []string{file},
		}
		targetsJSON, err := json.Marshal(targets)
		if err != nil {
			return nil, err
		}
		retryPolicyJSON, _ := json.Marshal(map[string]int{"max_attempts": rc.Config.RetryCap})

		tasks = append(tasks, core.TaskRecord{
			RunID:           rc.RunID,
			Tool:            p.ID(),
			TaskType:        "add_test",
			Priority:        50,
			Status:          "queued",
			Fingerprint:     hashTask("coverage", file),
			Title:           fmt.Sprintf("Improve coverage for %s", file),
			Description:     fmt.Sprintf("Add tests to raise coverage for %s.", file),
			TargetsJSON:     string(targetsJSON),
			RetryPolicyJSON: string(retryPolicyJSON),
			CreatedAt:       now,
			UpdatedAt:       now,
		})
	}

	return tasks, nil
}

func (p *Plugin) ValidationSpec(ctx context.Context, rc core.RunContext, task core.TaskRecord) (core.ValidationSpec, error) {
	var targets struct {
		Files []string `json:"files"`
	}
	if err := json.Unmarshal([]byte(task.TargetsJSON), &targets); err != nil {
		return core.ValidationSpec{}, err
	}

	profile, err := rc.RepoAdapter.Detect(rc.RepoPath)
	if err != nil {
		return core.ValidationSpec{}, err
	}
	invocation, err := rc.RepoAdapter.ResolvePython(profile)
	if err != nil {
		return core.ValidationSpec{}, err
	}

	args := []string{"-m", "pytest", "--cov=.", "--cov-report=json"}
	if len(targets.Files) > 0 {
		args[2] = "--cov=" + targets.Files[0]
	}
	command := core.CommandSpec{
		Runner: "python",
		Args:   invocation.Command(args...),
		Cwd:    rc.RepoPath,
	}

	return core.ValidationSpec{
		Commands: []core.CommandSpec{command},
		SuccessCriteria: core.SuccessCriteria{
			RequireExitCode0: true,
		},
	}, nil
}

func newArtifact(runID string, tool string, kind string, path string) core.ArtifactRecord {
	info, _ := os.Stat(path)
	size := int64(0)
	if info != nil {
		size = info.Size()
	}
	return core.ArtifactRecord{
		RunID:     runID,
		Tool:      tool,
		Kind:      kind,
		Path:      path,
		SizeBytes: size,
		CreatedAt: time.Now(),
	}
}

func hashCoverage(file string, percent float64) string {
	payload := fmt.Sprintf("%s|%.2f", file, percent)
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func hashTask(tool string, key string) string {
	sum := sha256.Sum256([]byte(tool + ":" + key))
	return hex.EncodeToString(sum[:])
}

type coverageReport struct {
	Files map[string]coverageFile `json:"files"`
}

type coverageFile struct {
	Summary coverageSummary `json:"summary"`
}

type coverageSummary struct {
	PercentCovered float64 `json:"percent_covered"`
}
