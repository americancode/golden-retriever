package npm

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
)

type stringList []string

func (l *stringList) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*l = nil
		return nil
	}
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		if one == "" {
			*l = nil
			return nil
		}
		*l = []string{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return err
	}
	*l = many
	return nil
}

type PackagePlatformError struct {
	Package string
	Field   string
	Value   string
	Allowed []string
}

func (e *PackagePlatformError) Error() string {
	return fmt.Sprintf("%s is incompatible with %s=%s allowed=%v", e.Package, e.Field, e.Value, e.Allowed)
}

func platformCompatible(manifest VersionManifest, opts ResolveOptions) (bool, *PackagePlatformError) {
	pkg := manifest.Name + "@" + manifest.Version
	osValue := npmOS(runtime.GOOS)
	if !matchesPlatformList(osValue, manifest.OS) {
		return false, &PackagePlatformError{Package: pkg, Field: "os", Value: osValue, Allowed: manifest.OS}
	}
	cpuValue := npmCPU(runtime.GOARCH)
	if !matchesPlatformList(cpuValue, manifest.CPU) {
		return false, &PackagePlatformError{Package: pkg, Field: "cpu", Value: cpuValue, Allowed: manifest.CPU}
	}
	if opts.Libc != "" && !matchesPlatformList(opts.Libc, manifest.Libc) {
		return false, &PackagePlatformError{Package: pkg, Field: "libc", Value: opts.Libc, Allowed: manifest.Libc}
	}
	return true, nil
}

func matchesPlatformList(value string, rules []string) bool {
	if len(rules) == 0 {
		return true
	}
	if len(rules) == 1 && strings.TrimSpace(rules[0]) == "any" {
		return true
	}
	allowAny := true
	allowed := false
	for _, rule := range rules {
		rule = strings.TrimSpace(rule)
		if rule == "" {
			continue
		}
		negated := strings.HasPrefix(rule, "!")
		if negated {
			rule = strings.TrimPrefix(rule, "!")
		} else {
			allowAny = false
		}
		if rule != value {
			continue
		}
		if negated {
			return false
		}
		allowed = true
	}
	return allowAny || allowed
}

func npmOS(goos string) string {
	if goos == "windows" {
		return "win32"
	}
	return goos
}

func npmCPU(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	case "arm":
		return "arm"
	case "arm64":
		return "arm64"
	default:
		return goarch
	}
}
