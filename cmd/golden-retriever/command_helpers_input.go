package main

import (
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func resolveInputs(input, inputs string) ([]string, error) {
	seen := map[string]struct{}{}
	var out []string
	add := func(v string) error {
		v = strings.TrimSpace(v)
		if v == "" {
			return nil
		}
		abs, err := filepath.Abs(v)
		if err != nil {
			return err
		}
		if _, ok := seen[abs]; ok {
			return nil
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
		return nil
	}
	hasInputsList := strings.TrimSpace(inputs) != ""
	trimmedInput := strings.TrimSpace(input)
	includePrimaryInput := true
	if hasInputsList && (trimmedInput == "" || (trimmedInput == "package.json" && !fileExists(trimmedInput))) {
		includePrimaryInput = false
	}
	if includePrimaryInput {
		if err := add(input); err != nil {
			return nil, err
		}
	}
	for _, part := range strings.Split(inputs, ",") {
		if err := add(part); err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}

func selectedBoolAlias(fs *flag.FlagSet, primary string, primaryValue bool, legacy string, legacyValue bool) bool {
	primarySet := false
	legacySet := false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case primary:
			primarySet = true
		case legacy:
			legacySet = true
		}
	})
	if primarySet {
		return primaryValue
	}
	if legacySet {
		return legacyValue
	}
	return primaryValue
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func projectSlug(input string) string {
	base := filepath.Base(input)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	dir := filepath.Base(filepath.Dir(input))
	slug := dir + "-" + base
	slug = strings.ToLower(slug)
	var b strings.Builder
	for _, r := range slug {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func multiProjectPaths(input, outBase, stateBase, metadataBase string) (string, string, string) {
	slug := projectSlug(input)
	out := filepath.Join(outBase, slug)
	metadata := filepath.Join(metadataBase, slug)
	state := stateBase
	if strings.HasSuffix(stateBase, ".json") {
		state = filepath.Join(filepath.Dir(stateBase), strings.TrimSuffix(filepath.Base(stateBase), ".json"), slug+".json")
	} else {
		state = filepath.Join(stateBase, slug+".json")
	}
	return out, state, metadata
}
