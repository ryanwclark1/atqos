package config

import (
	"encoding/json"
	"os"
)

type Config struct {
	MaxWorkers      int            `json:"max_workers"`
	MaxAgentWorkers int            `json:"max_agent_workers"`
	RetryCap        int            `json:"retry_cap"`
	CheckpointMins  int            `json:"checkpoint_minutes"`
	AllowedPaths    []string       `json:"allowed_paths"`
	GitStrategy     string         `json:"git_strategy"`
	Pytest          PluginConfig   `json:"pytest"`
	Coverage        CoverageConfig `json:"coverage"`
}

type PluginConfig struct {
	Enabled bool `json:"enabled"`
}

type CoverageConfig struct {
	Enabled          bool    `json:"enabled"`
	MinimumThreshold float64 `json:"minimum_threshold"`
}

func Default() Config {
	return Config{
		MaxWorkers:      4,
		MaxAgentWorkers: 2,
		RetryCap:        2,
		CheckpointMins:  30,
		AllowedPaths:    []string{"src", "tests"},
		GitStrategy:     "worktree",
		Pytest: PluginConfig{
			Enabled: true,
		},
		Coverage: CoverageConfig{
			Enabled:          true,
			MinimumThreshold: 0.9,
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}

	if err := json.Unmarshal(content, &cfg); err != nil {
		return cfg, err
	}

	return cfg, nil
}

func (c Config) PluginEnabled(id string) bool {
	switch id {
	case "pytest":
		return c.Pytest.Enabled
	case "coverage":
		return c.Coverage.Enabled
	default:
		return true
	}
}
