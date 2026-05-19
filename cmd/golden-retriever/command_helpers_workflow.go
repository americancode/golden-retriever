package main

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"golden-retriever/internal/npm"
)

func resolveProjectsParallel(ctx context.Context, inputs []string, workers int, progressf func(format string, args ...any), resolveFn func(input string) (*npm.Graph, error)) ([]npm.Package, map[string]int, error) {
	type result struct {
		input string
		graph *npm.Graph
		err   error
	}
	jobs := make(chan string)
	results := make(chan result, len(inputs))
	var wg sync.WaitGroup

	for i := 0; i < max(1, workers); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for input := range jobs {
				if progressf != nil {
					progressf("resolve:start input=%s", input)
				}
				graph, err := resolveFn(input)
				if err == nil && progressf != nil {
					progressf("resolve:done input=%s packages=%d", input, len(graph.Packages()))
				}
				results <- result{input: input, graph: graph, err: err}
			}
		}()
	}
	for _, input := range inputs {
		jobs <- input
	}
	close(jobs)
	wg.Wait()
	close(results)

	unique := map[string]npm.Package{}
	perProject := map[string]int{}
	for res := range results {
		if res.err != nil {
			return nil, nil, fmt.Errorf("%s: %w", res.input, res.err)
		}
		pkgs := res.graph.Packages()
		perProject[res.input] = len(pkgs)
		for _, pkg := range pkgs {
			unique[pkg.Key()] = pkg
		}
	}
	merged := make([]npm.Package, 0, len(unique))
	for _, pkg := range unique {
		merged = append(merged, pkg)
	}
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Key() < merged[j].Key()
	})
	return merged, perProject, nil
}
