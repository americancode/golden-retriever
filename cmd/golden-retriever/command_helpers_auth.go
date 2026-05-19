package main

import (
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	"golden-retriever/internal/npm"
)

func detectTargetAuthSource(registry string, cfg *npm.Config) string {
	if cfg == nil {
		return "none"
	}
	header := cfg.AuthFor(strings.TrimRight(registry, "/") + "/-/whoami").Header
	if header == "" {
		return "none"
	}
	checkBearer := []string{"NPM_TARGET_TOKEN", "NPM_AUTH_TOKEN", "NODE_AUTH_TOKEN", "NPM_TOKEN", "CI_JOB_TOKEN"}
	for _, key := range checkBearer {
		if v := os.Getenv(key); v != "" && header == "Bearer "+v {
			return key
		}
	}
	checkUserPass := [][2]string{
		{"NPM_TARGET_USERNAME", "NPM_TARGET_PASSWORD"},
		{"CI_DEPLOY_USER", "CI_DEPLOY_PASSWORD"},
		{"NPM_USERNAME", "NPM_PASSWORD"},
	}
	for _, pair := range checkUserPass {
		u := os.Getenv(pair[0])
		p := os.Getenv(pair[1])
		if u == "" || p == "" {
			continue
		}
		if header == "Basic "+base64.StdEncoding.EncodeToString([]byte(u+":"+p)) {
			return pair[0] + "/" + pair[1]
		}
	}
	return "npmrc"
}

func authHeaderKind(cfg *npm.Config, registry string) string {
	if cfg == nil {
		return "none"
	}
	header := cfg.AuthFor(strings.TrimRight(registry, "/") + "/-/whoami").Header
	switch {
	case strings.HasPrefix(header, "Bearer "):
		return "bearer"
	case strings.HasPrefix(header, "Basic "):
		return "basic"
	case header == "":
		return "none"
	default:
		return "other"
	}
}

func printEngineWarnings(graph *npm.Graph) {
	if graph == nil {
		return
	}
	for _, warning := range graph.EngineWarnings {
		if warning == nil {
			continue
		}
		fmt.Fprintf(os.Stderr, "warn EBADENGINE package=%s required=%s@%s current=%s\n",
			warning.Package, warning.Engine, warning.Wanted, warning.Current)
	}
}

func printDeprecationWarnings(graph *npm.Graph) {
	if graph == nil {
		return
	}
	for _, warning := range graph.DeprecationWarnings {
		fmt.Fprintf(os.Stderr, "warn deprecated package=%s message=%s\n", warning.Package, warning.Message)
	}
}
