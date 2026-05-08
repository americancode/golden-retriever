package npm

import (
	"encoding/json"
	"fmt"
)

type dependencyBundle struct {
	All   bool
	Names map[string]struct{}
}

func (b *dependencyBundle) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*b = dependencyBundle{}
		return nil
	}
	var names []string
	if err := json.Unmarshal(data, &names); err == nil {
		b.All = false
		b.Names = map[string]struct{}{}
		for _, name := range names {
			if name != "" {
				b.Names[name] = struct{}{}
			}
		}
		return nil
	}
	var all bool
	if err := json.Unmarshal(data, &all); err == nil {
		b.All = all
		if all && b.Names == nil {
			b.Names = map[string]struct{}{}
		}
		return nil
	}
	return fmt.Errorf("bundleDependencies must be an array or boolean")
}

func (b dependencyBundle) MarshalJSON() ([]byte, error) {
	if b.All {
		return []byte("true"), nil
	}
	if len(b.Names) == 0 {
		return []byte("null"), nil
	}
	names := make([]string, 0, len(b.Names))
	for name := range b.Names {
		names = append(names, name)
	}
	return json.Marshal(names)
}

func bundledDependencyNames(manifest VersionManifest) dependencyBundle {
	if manifest.BundleDependencies.All || len(manifest.BundleDependencies.Names) > 0 {
		return manifest.BundleDependencies
	}
	return manifest.BundledDependencies
}

func filterBundledDependencies(deps map[string]string, bundled dependencyBundle) map[string]string {
	if len(deps) == 0 {
		return deps
	}
	if bundled.All {
		return nil
	}
	if len(bundled.Names) == 0 {
		return deps
	}
	filtered := map[string]string{}
	for name, spec := range deps {
		if _, ok := bundled.Names[name]; ok {
			continue
		}
		filtered[name] = spec
	}
	return filtered
}
