package main

import (
	"os"
	"strings"
	"testing"
)

func TestGitLabCIExampleCachesGoldenRetrieverPaths(t *testing.T) {
	data, err := os.ReadFile("../../.gitlab-ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		".gr/state.json",
		".gr/metadata/",
		".gr/tgzs/",
		"golden-retriever mirror",
		"$NPM_TARGET_REGISTRY",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf(".gitlab-ci.yml missing %q", want)
		}
	}
}
