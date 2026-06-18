package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"golden-retriever/internal/npm"
)

type resolveWorkItem struct {
	input    string
	platform *npm.NPMPlatform
}

func resolveProjectsParallel(ctx context.Context, inputs []string, workers int, platforms []npm.NPMPlatform, progressf func(format string, args ...any), resolveFn func(input string, platform *npm.NPMPlatform) (*npm.Graph, error)) ([]npm.Package, map[string]int, error) {
	type result struct {
		job   resolveWorkItem
		graph *npm.Graph
		err   error
	}
	jobsList, err := buildResolveWorkItems(inputs, platforms)
	if err != nil {
		return nil, nil, err
	}
	jobs := make(chan resolveWorkItem)
	results := make(chan result, len(jobsList))
	var wg sync.WaitGroup

	for i := 0; i < max(1, workers); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobs {
				if progressf != nil {
					if job.platform != nil {
						progressf("resolve:start input=%s platform=%s", job.input, job.platform.Label())
					} else {
						progressf("resolve:start input=%s", job.input)
					}
				}
				graph, err := resolveFn(job.input, job.platform)
				if err == nil && progressf != nil {
					if job.platform != nil {
						progressf("resolve:done input=%s platform=%s packages=%d", job.input, job.platform.Label(), len(graph.Packages()))
					} else {
						progressf("resolve:done input=%s packages=%d", job.input, len(graph.Packages()))
					}
				}
				results <- result{job: job, graph: graph, err: err}
			}
		}()
	}
	for _, job := range jobsList {
		jobs <- job
	}
	close(jobs)
	wg.Wait()
	close(results)

	unique := map[string]npm.Package{}
	perProject := map[string]map[string]npm.Package{}
	for res := range results {
		if res.err != nil {
			return nil, nil, fmt.Errorf("%s: %w", res.job.input, res.err)
		}
		pkgs := res.graph.Packages()
		projectUnique := perProject[res.job.input]
		if projectUnique == nil {
			projectUnique = map[string]npm.Package{}
			perProject[res.job.input] = projectUnique
		}
		for _, pkg := range pkgs {
			unique[pkg.Key()] = pkg
			projectUnique[pkg.Key()] = pkg
		}
	}
	merged := make([]npm.Package, 0, len(unique))
	for _, pkg := range unique {
		merged = append(merged, pkg)
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Key() < merged[j].Key()
	})
	perProjectCounts := make(map[string]int, len(perProject))
	for input, pkgs := range perProject {
		perProjectCounts[input] = len(pkgs)
	}
	return merged, perProjectCounts, nil
}

func buildResolveWorkItems(inputs []string, platforms []npm.NPMPlatform) ([]resolveWorkItem, error) {
	if len(platforms) == 0 {
		jobs := make([]resolveWorkItem, 0, len(inputs))
		for _, input := range inputs {
			jobs = append(jobs, resolveWorkItem{input: input})
		}
		return jobs, nil
	}
	jobs := make([]resolveWorkItem, 0, len(inputs)*len(platforms))
	for _, input := range inputs {
		usesPlatforms, err := inputUsesNPMPlatforms(input)
		if err != nil {
			return nil, err
		}
		if !usesPlatforms {
			jobs = append(jobs, resolveWorkItem{input: input})
			continue
		}
		for _, platform := range platforms {
			platform := platform
			jobs = append(jobs, resolveWorkItem{input: input, platform: &platform})
		}
	}
	return jobs, nil
}

func inputUsesNPMPlatforms(input string) (bool, error) {
	info, err := os.Stat(input)
	if err == nil && info.IsDir() {
		if fileExists(filepath.Join(input, "npm-shrinkwrap.json")) || fileExists(filepath.Join(input, "package-lock.json")) {
			return false, nil
		}
		return true, nil
	}
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	base := filepath.Base(input)
	if base == "package-lock.json" || base == "npm-shrinkwrap.json" {
		return false, nil
	}
	lockfile, err := looksLikeLockfile(input)
	if err != nil {
		return false, err
	}
	return !lockfile, nil
}

func looksLikeLockfile(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	var probe struct {
		LockfileVersion *int `json:"lockfileVersion"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return false, nil
	}
	return probe.LockfileVersion != nil, nil
}
