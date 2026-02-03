package agent

import (
	"context"
	"fmt"
)

type LocalAdapter struct{}

func NewLocal() *LocalAdapter {
	return &LocalAdapter{}
}

func (a *LocalAdapter) Name() string {
	return "local"
}

func (a *LocalAdapter) Invoke(ctx context.Context, req Request) (Result, error) {
	if req.TaskID == 0 {
		return Result{}, fmt.Errorf("task id required")
	}
	return Result{
		SchemaVersion: req.SchemaVersion,
		RunID:         req.RunID,
		TaskID:        req.TaskID,
		Status:        "success",
		Summary:       "no-op agent invocation",
	}, nil
}
