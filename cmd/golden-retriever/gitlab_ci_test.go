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
		"resource_group: \"golden-retriever-state-${CI_PROJECT_ID}-${CI_COMMIT_REF_SLUG}\"",
		"policy: pull",
		"golden-retriever mirror",
		"golden-retriever state sync-target --state \"$GOLDEN_RETRIEVER_STATE\" --target-registry \"$NPM_TARGET_REGISTRY\"",
		"$NPM_TARGET_REGISTRY",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf(".gitlab-ci.yml missing %q", want)
		}
	}
}
