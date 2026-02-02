package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"atqos/internal/core"
)

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLite(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Init(ctx context.Context) error {
	ddl := []string{
		`PRAGMA journal_mode=WAL;`,
		`PRAGMA foreign_keys=ON;`,
		`CREATE TABLE IF NOT EXISTS runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL UNIQUE,
			repo_path TEXT NOT NULL,
			started_at TEXT NOT NULL,
			finished_at TEXT,
			status TEXT NOT NULL,
			config_json TEXT NOT NULL,
			summary_json TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS artifacts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL,
			tool TEXT,
			kind TEXT NOT NULL,
			path TEXT NOT NULL,
			sha256 TEXT,
			size_bytes INTEGER,
			created_at TEXT NOT NULL,
			meta_json TEXT,
			FOREIGN KEY(run_id) REFERENCES runs(run_id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_artifacts_run_id ON artifacts(run_id);`,
		`CREATE TABLE IF NOT EXISTS findings (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL,
			tool TEXT NOT NULL,
			kind TEXT NOT NULL,
			severity TEXT NOT NULL,
			fingerprint TEXT NOT NULL,
			message TEXT NOT NULL,
			file_path TEXT,
			line INTEGER,
			col INTEGER,
			symbol TEXT,
			test_id TEXT,
			raw_ref TEXT,
			meta_json TEXT,
			created_at TEXT NOT NULL,
			FOREIGN KEY(run_id) REFERENCES runs(run_id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_findings_run_tool ON findings(run_id, tool);`,
		`CREATE INDEX IF NOT EXISTS idx_findings_fingerprint ON findings(fingerprint);`,
		`CREATE TABLE IF NOT EXISTS tasks (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL,
			tool TEXT NOT NULL,
			task_type TEXT NOT NULL,
			priority INTEGER NOT NULL,
			status TEXT NOT NULL,
			fingerprint TEXT NOT NULL,
			title TEXT NOT NULL,
			description TEXT,
			targets_json TEXT NOT NULL,
			validation_json TEXT NOT NULL,
			retry_policy_json TEXT NOT NULL,
			depends_on_json TEXT,
			extra_json TEXT,
			claimed_by TEXT,
			claimed_at TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			FOREIGN KEY(run_id) REFERENCES runs(run_id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_run_status ON tasks(run_id, status);`,
		`CREATE INDEX IF NOT EXISTS idx_tasks_fingerprint ON tasks(fingerprint);`,
		`CREATE TABLE IF NOT EXISTS attempts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			task_id INTEGER NOT NULL,
			attempt_no INTEGER NOT NULL,
			status TEXT NOT NULL,
			agent_name TEXT,
			agent_exit_code INTEGER,
			validation_exit_code INTEGER,
			started_at TEXT NOT NULL,
			finished_at TEXT,
			summary_json TEXT,
			diff_stats_json TEXT,
			artifacts_json TEXT,
			FOREIGN KEY(task_id) REFERENCES tasks(id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_attempts_task_id ON attempts(task_id);`,
		`CREATE TABLE IF NOT EXISTS task_findings (
			task_id INTEGER NOT NULL,
			finding_id INTEGER NOT NULL,
			PRIMARY KEY(task_id, finding_id),
			FOREIGN KEY(task_id) REFERENCES tasks(id),
			FOREIGN KEY(finding_id) REFERENCES findings(id)
		);`,
	}

	for _, stmt := range ddl {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}

	return nil
}

func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLiteStore) CreateRun(ctx context.Context, run core.RunRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO runs (run_id, repo_path, started_at, status, config_json)
		VALUES (?, ?, ?, ?, ?)`,
		run.RunID,
		run.RepoPath,
		run.StartedAt.UTC().Format(time.RFC3339),
		run.Status,
		run.Config,
	)
	return err
}

func (s *SQLiteStore) UpdateRunStatus(ctx context.Context, runID string, status string, summaryJSON string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE runs
		SET status = ?, summary_json = ?, finished_at = ?
		WHERE run_id = ?`,
		status,
		summaryJSON,
		time.Now().UTC().Format(time.RFC3339),
		runID,
	)
	return err
}

func (s *SQLiteStore) AddArtifact(ctx context.Context, artifact core.ArtifactRecord) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO artifacts (run_id, tool, kind, path, sha256, size_bytes, created_at, meta_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		artifact.RunID,
		artifact.Tool,
		artifact.Kind,
		artifact.Path,
		artifact.SHA256,
		artifact.SizeBytes,
		artifact.CreatedAt.UTC().Format(time.RFC3339),
		artifact.MetaJSON,
	)
	return err
}

func (s *SQLiteStore) InsertFindings(ctx context.Context, findings []core.FindingRecord) error {
	if len(findings) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO findings (run_id, tool, kind, severity, fingerprint, message, file_path, line, col, symbol, test_id, raw_ref, meta_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, finding := range findings {
		if _, err := stmt.ExecContext(ctx,
			finding.RunID,
			finding.Tool,
			finding.Kind,
			finding.Severity,
			finding.Fingerprint,
			finding.Message,
			finding.FilePath,
			finding.Line,
			finding.Column,
			finding.Symbol,
			finding.TestID,
			finding.RawRef,
			finding.MetaJSON,
			finding.CreatedAt.UTC().Format(time.RFC3339),
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *SQLiteStore) InsertTasks(ctx context.Context, tasks []core.TaskRecord) error {
	if len(tasks) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO tasks (run_id, tool, task_type, priority, status, fingerprint, title, description, targets_json, validation_json, retry_policy_json, depends_on_json, extra_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, task := range tasks {
		if _, err := stmt.ExecContext(ctx,
			task.RunID,
			task.Tool,
			task.TaskType,
			task.Priority,
			task.Status,
			task.Fingerprint,
			task.Title,
			task.Description,
			task.TargetsJSON,
			task.ValidationJSON,
			task.RetryPolicyJSON,
			task.DependsOnJSON,
			task.ExtraJSON,
			task.CreatedAt.UTC().Format(time.RFC3339),
			task.UpdatedAt.UTC().Format(time.RFC3339),
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *SQLiteStore) ClaimNextTask(ctx context.Context, runID string, workerID string) (*core.TaskRecord, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `
		SELECT id, tool, task_type, priority, status, fingerprint, title, description,
		       targets_json, validation_json, retry_policy_json, depends_on_json, extra_json,
		       claimed_by, claimed_at, created_at, updated_at
		FROM tasks
		WHERE run_id = ? AND status = 'queued'
		ORDER BY priority DESC, created_at ASC
		LIMIT 1`,
		runID,
	)

	var (
		task      core.TaskRecord
		claimedBy sql.NullString
		claimedAt sql.NullString
		createdAt string
		updatedAt string
	)

	err = row.Scan(
		&task.ID,
		&task.Tool,
		&task.TaskType,
		&task.Priority,
		&task.Status,
		&task.Fingerprint,
		&task.Title,
		&task.Description,
		&task.TargetsJSON,
		&task.ValidationJSON,
		&task.RetryPolicyJSON,
		&task.DependsOnJSON,
		&task.ExtraJSON,
		&claimedBy,
		&claimedAt,
		&createdAt,
		&updatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	task.RunID = runID
	if claimedBy.Valid {
		task.ClaimedBy = claimedBy.String
	}
	if claimedAt.Valid {
		task.ClaimedAt, _ = time.Parse(time.RFC3339, claimedAt.String)
	}
	task.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	task.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)

	claimedAtText := time.Now().UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(ctx, `
		UPDATE tasks
		SET status = 'running', claimed_by = ?, claimed_at = ?, updated_at = ?
		WHERE id = ? AND status = 'queued'`,
		workerID,
		claimedAtText,
		claimedAtText,
		task.ID,
	); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	task.Status = "running"
	task.ClaimedBy = workerID
	task.ClaimedAt, _ = time.Parse(time.RFC3339, claimedAtText)
	task.UpdatedAt = task.ClaimedAt
	return &task, nil
}

func (s *SQLiteStore) UpdateTaskStatus(ctx context.Context, taskID int64, status string, extraJSON string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE tasks
		SET status = ?, extra_json = ?, updated_at = ?
		WHERE id = ?`,
		status,
		extraJSON,
		time.Now().UTC().Format(time.RFC3339),
		taskID,
	)
	return err
}

func (s *SQLiteStore) CreateAttempt(ctx context.Context, attempt core.AttemptRecord) (int64, error) {
	result, err := s.db.ExecContext(ctx, `
		INSERT INTO attempts (task_id, attempt_no, status, agent_name, agent_exit_code, validation_exit_code, started_at, summary_json, diff_stats_json, artifacts_json)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		attempt.TaskID,
		attempt.AttemptNo,
		attempt.Status,
		attempt.AgentName,
		attempt.AgentExitCode,
		attempt.ValidationExitCode,
		attempt.StartedAt.UTC().Format(time.RFC3339),
		attempt.SummaryJSON,
		attempt.DiffStatsJSON,
		attempt.ArtifactsJSON,
	)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *SQLiteStore) FinishAttempt(ctx context.Context, attemptID int64, status string, summaryJSON string, validationExitCode int) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE attempts
		SET status = ?, summary_json = ?, validation_exit_code = ?, finished_at = ?
		WHERE id = ?`,
		status,
		summaryJSON,
		validationExitCode,
		time.Now().UTC().Format(time.RFC3339),
		attemptID,
	)
	return err
}

func (s *SQLiteStore) GetRunSummary(ctx context.Context, runID string) (core.RunSummary, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT status, started_at, finished_at
		FROM runs
		WHERE run_id = ?`,
		runID,
	)

	var (
		status     string
		startedAt  string
		finishedAt sql.NullString
	)
	if err := row.Scan(&status, &startedAt, &finishedAt); err != nil {
		return core.RunSummary{}, err
	}

	var findingCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM findings WHERE run_id = ?`, runID).Scan(&findingCount); err != nil {
		return core.RunSummary{}, err
	}

	var taskCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tasks WHERE run_id = ?`, runID).Scan(&taskCount); err != nil {
		return core.RunSummary{}, err
	}

	started, _ := time.Parse(time.RFC3339, startedAt)
	finished := time.Time{}
	if finishedAt.Valid {
		finished, _ = time.Parse(time.RFC3339, finishedAt.String)
	}

	return core.RunSummary{
		RunID:    runID,
		Status:   status,
		Findings: findingCount,
		Tasks:    taskCount,
		Started:  started,
		Finished: finished,
	}, nil
}
