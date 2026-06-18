package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"golden-retriever/internal/npm"
)

func TestBuildResolveWorkItemsExpandsPlatformsAtTopLevel(t *testing.T) {
	t.Parallel()

	packageDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(packageDir, "package.json"), []byte(`{"name":"pkg","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	lockDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(lockDir, "package-lock.json"), []byte(`{"lockfileVersion":3}`), 0o644); err != nil {
		t.Fatal(err)
	}

	packageFile := filepath.Join(t.TempDir(), "package.json")
	if err := os.WriteFile(packageFile, []byte(`{"name":"pkg-file","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	platforms := []npm.NPMPlatform{
		{OS: "linux", CPU: "x64"},
		{OS: "darwin", CPU: "arm64"},
	}

	jobs, err := buildResolveWorkItems([]string{packageDir, lockDir, packageFile}, platforms)
	if err != nil {
		t.Fatal(err)
	}

	if len(jobs) != 5 {
		t.Fatalf("len(jobs) = %d, want 5", len(jobs))
	}
	if jobs[0].input != packageDir || jobs[0].platform == nil || jobs[0].platform.Label() != "linux/x64" {
		t.Fatalf("jobs[0] = %#v", jobs[0])
	}
	if jobs[1].input != packageDir || jobs[1].platform == nil || jobs[1].platform.Label() != "darwin/arm64" {
		t.Fatalf("jobs[1] = %#v", jobs[1])
	}
	if jobs[2].input != lockDir || jobs[2].platform != nil {
		t.Fatalf("jobs[2] = %#v", jobs[2])
	}
	if jobs[3].input != packageFile || jobs[3].platform == nil || jobs[3].platform.Label() != "linux/x64" {
		t.Fatalf("jobs[3] = %#v", jobs[3])
	}
	if jobs[4].input != packageFile || jobs[4].platform == nil || jobs[4].platform.Label() != "darwin/arm64" {
		t.Fatalf("jobs[4] = %#v", jobs[4])
	}
}

func TestResolveProjectsParallelMergesPlatformResultsPerInput(t *testing.T) {
	t.Parallel()

	input := filepath.Join(t.TempDir(), "package.json")
	if err := os.WriteFile(input, []byte(`{"name":"pkg","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	platforms := []npm.NPMPlatform{
		{OS: "linux", CPU: "x64"},
		{OS: "darwin", CPU: "arm64"},
	}

	packages, perProject, err := resolveProjectsParallel(context.Background(), []string{input}, 2, platforms, nil, func(currentInput string, platform *npm.NPMPlatform) (*npm.Graph, error) {
		graph := npm.NewGraph()
		graph.Add(npm.Package{Name: "shared", Version: "1.0.0"})
		if platform != nil {
			graph.Add(npm.Package{Name: "platform-" + platform.OS, Version: "1.0.0"})
		}
		return graph, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(packages) != 3 {
		t.Fatalf("len(packages) = %d, want 3", len(packages))
	}
	if perProject[input] != 3 {
		t.Fatalf("perProject[%q] = %d, want 3", input, perProject[input])
	}
}
