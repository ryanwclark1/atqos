package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"atqos/internal/app"
)

func main() {
	root := flag.String("repo", ".", "Path to repository root")
	artifacts := flag.String("artifacts", "artifacts", "Artifact output directory")
	dbPath := flag.String("db", "artifacts/atqos.db", "SQLite database path")
	configPath := flag.String("config", "", "Optional config file path")
	flag.Parse()

	repoPath, err := filepath.Abs(*root)
	if err != nil {
		log.Fatalf("failed to resolve repo path: %v", err)
	}

	artifactRoot, err := filepath.Abs(*artifacts)
	if err != nil {
		log.Fatalf("failed to resolve artifact path: %v", err)
	}

	dbFile, err := filepath.Abs(*dbPath)
	if err != nil {
		log.Fatalf("failed to resolve db path: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := app.Command{
		RepoPath:    repoPath,
		ArtifactDir: artifactRoot,
		DBPath:      dbFile,
		ConfigPath:  *configPath,
	}

	result, err := cmd.Run(ctx)
	if err != nil {
		log.Fatalf("run failed: %v", err)
	}

	fmt.Printf("Run %s finished with status %s\n", result.RunID, result.Status)
	if result.Summary != "" {
		fmt.Println(result.Summary)
	}
}
