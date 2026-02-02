package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"
)

const (
	RunStatusRunning   = "running"
	RunStatusSucceeded = "succeeded"
	RunStatusFailed    = "failed"
)

type RunRecord struct {
	RunID     string
	RepoPath  string
	StartedAt time.Time
	Status    string
	Config    string
}

type ArtifactRecord struct {
	RunID     string
	Tool      string
	Kind      string
	Path      string
	SHA256    string
	SizeBytes int64
	CreatedAt time.Time
	MetaJSON  string
}

type FindingRecord struct {
	RunID       string
	Tool        string
	Kind        string
	Severity    string
	Fingerprint string
	Message     string
	FilePath    string
	Line        int
	Column      int
	Symbol      string
	TestID      string
	RawRef      string
	MetaJSON    string
	CreatedAt   time.Time
}

type TaskRecord struct {
	ID              int64
	RunID           string
	Tool            string
	TaskType        string
	Priority        int
	Status          string
	Fingerprint     string
	Title           string
	Description     string
	TargetsJSON     string
	ValidationJSON  string
	RetryPolicyJSON string
	DependsOnJSON   string
	ExtraJSON       string
	ClaimedBy       string
	ClaimedAt       time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type AttemptRecord struct {
	ID                 int64
	TaskID             int64
	AttemptNo          int
	Status             string
	AgentName          string
	AgentExitCode      int
	ValidationExitCode int
	StartedAt          time.Time
	FinishedAt         time.Time
	SummaryJSON        string
	DiffStatsJSON      string
	ArtifactsJSON      string
}

type CommandSpec struct {
	Runner         string            `json:"runner"`
	Args           []string          `json:"args"`
	Env            map[string]string `json:"env,omitempty"`
	Cwd            string            `json:"cwd,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}

type ValidationSpec struct {
	Commands        []CommandSpec   `json:"commands"`
	SuccessCriteria SuccessCriteria `json:"success_criteria"`
}

type SuccessCriteria struct {
	RequireExitCode0 bool    `json:"require_exit_code_0"`
	MaxNewFindings   int     `json:"max_new_findings,omitempty"`
	MinCoverageDelta float64 `json:"min_coverage_delta,omitempty"`
}

type ArtifactSet struct {
	PluginID string
	Items    []ArtifactRecord
}

type Summary struct {
	Findings int
	Tasks    int
}

type RunSummary struct {
	RunID    string
	Status   string
	Findings int
	Tasks    int
	Started  time.Time
	Finished time.Time
}

func (s *Summary) Add(findings []FindingRecord, tasks []TaskRecord) {
	s.Findings += len(findings)
	s.Tasks += len(tasks)
}

func (s Summary) String() string {
	out, _ := json.Marshal(s)
	return string(out)
}

func NewRunID() (string, error) {
	bytes := make([]byte, 8)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate run id: %w", err)
	}
	return fmt.Sprintf("run-%s", hex.EncodeToString(bytes)), nil
}
