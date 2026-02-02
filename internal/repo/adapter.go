package repo

import (
	"os"
	"path/filepath"
)

type Adapter struct{}

func NewAdapter() *Adapter {
	return &Adapter{}
}

type Profile struct {
	RepoPath      string
	PythonManager string
	HasUVLock     bool
	HasPoetryLock bool
	VenvPath      string
}

type PythonInvocation struct {
	PythonPath string
	Tool       string
	PrefixArgs []string
}

func (a *Adapter) Detect(repoPath string) (Profile, error) {
	profile := Profile{RepoPath: repoPath}

	uvLock := filepath.Join(repoPath, "uv.lock")
	if exists(uvLock) {
		profile.HasUVLock = true
		profile.PythonManager = "uv"
	}

	poetryLock := filepath.Join(repoPath, "poetry.lock")
	if exists(poetryLock) {
		profile.HasPoetryLock = true
		if profile.PythonManager == "" {
			profile.PythonManager = "poetry"
		}
	}

	venvPath := filepath.Join(repoPath, ".venv", "bin", "python")
	if exists(venvPath) {
		profile.VenvPath = venvPath
		if profile.PythonManager == "" {
			profile.PythonManager = "venv"
		}
	}

	if profile.PythonManager == "" {
		profile.PythonManager = "system"
	}

	return profile, nil
}

func (a *Adapter) ResolvePython(profile Profile) (PythonInvocation, error) {
	if profile.VenvPath != "" {
		return PythonInvocation{PythonPath: profile.VenvPath}, nil
	}

	if profile.HasUVLock {
		return PythonInvocation{Tool: "uv", PrefixArgs: []string{"run", "python"}}, nil
	}

	return PythonInvocation{PythonPath: "python"}, nil
}

func (p PythonInvocation) Command(args ...string) []string {
	if p.PythonPath != "" {
		return append([]string{p.PythonPath}, args...)
	}
	if p.Tool != "" {
		parts := append([]string{p.Tool}, p.PrefixArgs...)
		return append(parts, args...)
	}
	return args
}

func exists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
