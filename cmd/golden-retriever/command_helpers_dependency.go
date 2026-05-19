package main

import (
	"fmt"
	"strings"
	"time"

	"golden-retriever/internal/npm"
)

type dependencySet struct {
	includeDev      bool
	includeOptional bool
	omitPeer        bool
}

func dependencySelection(includeDev, includeOptional bool, omit, include string) (dependencySet, error) {
	set := dependencySet{
		includeDev:      includeDev,
		includeOptional: includeOptional,
	}
	for _, item := range dependencyTypes(omit) {
		switch item {
		case "dev":
			set.includeDev = false
		case "optional":
			set.includeOptional = false
		case "peer":
			set.omitPeer = true
		default:
			return dependencySet{}, fmt.Errorf("unsupported omit dependency type %q", item)
		}
	}
	for _, item := range dependencyTypes(include) {
		switch item {
		case "dev":
			set.includeDev = true
		case "optional":
			set.includeOptional = true
		case "peer":
			set.omitPeer = false
		default:
			return dependencySet{}, fmt.Errorf("unsupported include dependency type %q", item)
		}
	}
	return set, nil
}

func dependencyTypes(value string) []string {
	var out []string
	for _, item := range strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n'
	}) {
		item = strings.TrimSpace(item)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func parseBefore(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	before, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid --before %q: expected RFC3339 timestamp", value)
	}
	return before, nil
}

func parseNPMPlatforms(raw string) ([]npm.NPMPlatform, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	platforms := []npm.NPMPlatform{}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts := strings.Split(item, "/")
		if len(parts) < 2 || len(parts) > 3 {
			return nil, fmt.Errorf("invalid npm platform %q: expected os/cpu or os/cpu/libc", item)
		}
		platform := npm.NPMPlatform{
			OS:  strings.TrimSpace(parts[0]),
			CPU: strings.TrimSpace(parts[1]),
		}
		if len(parts) == 3 {
			platform.Libc = strings.TrimSpace(parts[2])
		}
		if platform.OS == "" || platform.CPU == "" {
			return nil, fmt.Errorf("invalid npm platform %q: os and cpu are required", item)
		}
		platforms = append(platforms, platform)
	}
	return platforms, nil
}

func splitPackageKey(key string) (string, string, error) {
	if key == "" {
		return "", "", fmt.Errorf("missing --package")
	}
	start := 0
	if key[0] == '@' {
		start = 1
	}
	for i := len(key) - 1; i >= start; i-- {
		if key[i] == '@' {
			name := key[:i]
			version := key[i+1:]
			if name == "" || version == "" {
				break
			}
			return name, version, nil
		}
	}
	return "", "", fmt.Errorf("package must be name@version")
}
