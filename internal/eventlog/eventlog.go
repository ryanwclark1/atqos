package eventlog

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"atqos/internal/core"
)

type EventLog struct {
	mu   sync.Mutex
	file *os.File
}

func New(path string) (*EventLog, error) {
	if err := os.MkdirAll(filepathDir(path), 0o755); err != nil {
		return nil, err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open event log: %w", err)
	}

	return &EventLog{file: file}, nil
}

func (l *EventLog) Emit(event core.Event) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	payload := struct {
		TS string `json:"ts"`
		core.Event
	}{
		TS:    time.Now().UTC().Format(time.RFC3339),
		Event: event,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	if _, err := l.file.Write(append(data, '\n')); err != nil {
		return err
	}

	return nil
}

func (l *EventLog) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	return l.file.Close()
}

func filepathDir(path string) string {
	dir := filepath.Dir(path)
	if dir == "." {
		return ""
	}
	return dir
}
