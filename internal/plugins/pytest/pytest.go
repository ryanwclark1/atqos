package pytest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"atqos/internal/core"
	"atqos/internal/runner"
)

type Plugin struct{}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) ID() string {
	return "pytest"
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

	outputDir := filepath.Join(rc.ArtifactRoot, "pytest")
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return core.ArtifactSet{}, err
	}

	reportPath := filepath.Join(outputDir, "report.json")
	junitPath := filepath.Join(outputDir, "junit.xml")
	stdoutPath := filepath.Join(outputDir, "stdout.log")
	stderrPath := filepath.Join(outputDir, "stderr.log")

	args := invocation.Command(
		"-m", "pytest",
		"--json-report",
		"--json-report-file="+reportPath,
		"--junitxml="+junitPath,
		"-ra",
		"--showlocals",
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
		newArtifact(rc.RunID, p.ID(), "junit", junitPath),
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
		return nil, fmt.Errorf("pytest report artifact missing")
	}

	data, err := os.ReadFile(reportPath)
	if err != nil {
		return nil, err
	}

	var report pytestReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, err
	}

	now := time.Now()
	findings := make([]core.FindingRecord, 0)
	for _, test := range report.Tests {
		if test.Outcome != "failed" && test.Outcome != "error" {
			continue
		}
		message := test.Longrepr.String()
		if message == "" {
			message = test.Call.Crash.Message
		}

		severity := "high"
		if test.Outcome == "error" {
			severity = "blocker"
		}

		findings = append(findings, core.FindingRecord{
			RunID:       rc.RunID,
			Tool:        p.ID(),
			Kind:        "test_failure",
			Severity:    severity,
			Fingerprint: hashFinding(test.NodeID, test.Outcome, message),
			Message:     message,
			FilePath:    nodeIDFile(test.NodeID),
			TestID:      test.NodeID,
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

	byFile := make(map[string][]core.FindingRecord)
	for _, finding := range findings {
		file := finding.FilePath
		if file == "" {
			file = "unknown"
		}
		byFile[file] = append(byFile[file], finding)
	}

	files := make([]string, 0, len(byFile))
	for file := range byFile {
		files = append(files, file)
	}
	sort.Strings(files)

	now := time.Now()
	tasks := make([]core.TaskRecord, 0, len(files))
	for _, file := range files {
		fileFindings := byFile[file]
		testIDs := make([]string, 0, len(fileFindings))
		for _, finding := range fileFindings {
			if finding.TestID != "" {
				testIDs = append(testIDs, finding.TestID)
			}
		}
		targets := map[string]interface{}{
			"files":    []string{file},
			"test_ids": testIDs,
		}
		targetsJSON, err := json.Marshal(targets)
		if err != nil {
			return nil, err
		}
		retryPolicyJSON, _ := json.Marshal(map[string]int{"max_attempts": rc.Config.RetryCap})

		tasks = append(tasks, core.TaskRecord{
			RunID:           rc.RunID,
			Tool:            p.ID(),
			TaskType:        "fix",
			Priority:        100,
			Status:          "queued",
			Fingerprint:     hashTask("pytest", file),
			Title:           fmt.Sprintf("Fix pytest failures in %s", file),
			Description:     fmt.Sprintf("Resolve pytest failures for %s.", file),
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
		Files   []string `json:"files"`
		TestIDs []string `json:"test_ids"`
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

	args := []string{"-m", "pytest", "-q"}
	if len(targets.TestIDs) > 0 {
		args = append(args, targets.TestIDs...)
	} else if len(targets.Files) > 0 {
		args = append(args, targets.Files...)
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
	sum, _ := fileSHA256(path)
	return core.ArtifactRecord{
		RunID:     runID,
		Tool:      tool,
		Kind:      kind,
		Path:      path,
		SHA256:    sum,
		SizeBytes: size,
		CreatedAt: time.Now(),
	}
}

func fileSHA256(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func hashFinding(nodeID string, outcome string, message string) string {
	payload := strings.Join([]string{nodeID, outcome, message}, "|")
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func hashTask(tool string, key string) string {
	sum := sha256.Sum256([]byte(tool + ":" + key))
	return hex.EncodeToString(sum[:])
}

type pytestReport struct {
	Tests []pytestTest `json:"tests"`
}

type pytestTest struct {
	NodeID   string           `json:"nodeid"`
	Outcome  string           `json:"outcome"`
	Longrepr pytestLongrepr   `json:"longrepr"`
	Call     pytestCallResult `json:"call"`
}

type pytestLongrepr struct {
	Reprcrash struct {
		Message string `json:"message"`
	} `json:"reprcrash"`
	Reprtraceback struct {
		Reprentries []struct {
			Data string `json:"data"`
		} `json:"reprentries"`
	} `json:"reprtraceback"`
}

func (l pytestLongrepr) String() string {
	if l.Reprcrash.Message != "" {
		return l.Reprcrash.Message
	}
	if len(l.Reprtraceback.Reprentries) > 0 {
		return l.Reprtraceback.Reprentries[0].Data
	}
	return ""
}

type pytestCallResult struct {
	Crash struct {
		Message string `json:"message"`
	} `json:"crash"`
}

func nodeIDFile(nodeID string) string {
	if nodeID == "" {
		return ""
	}
	parts := strings.Split(nodeID, "::")
	return parts[0]
}
