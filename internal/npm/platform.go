package npm

import (
	"encoding/json"
	"fmt"
	"os"
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
	libcValue := effectiveLibc(runtime.GOOS, opts.Libc)
	if libcValue != "" && !matchesPlatformList(libcValue, manifest.Libc) {
		return false, &PackagePlatformError{Package: pkg, Field: "libc", Value: libcValue, Allowed: manifest.Libc}
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

func effectiveLibc(goos, configured string) string {
	if configured != "" {
		return configured
	}
	if goos != "linux" {
		return ""
	}
	return detectLinuxLibc()
}

func detectLinuxLibc() string {
	for _, path := range []string{"/usr/bin/ldd", "/bin/ldd"} {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if libc := classifyLibcOutput(string(data)); libc != "" {
			return libc
		}
	}
	if entries, err := os.ReadDir("/lib"); err == nil {
		for _, entry := range entries {
			name := strings.ToLower(entry.Name())
			if strings.Contains(name, "musl") {
				return "musl"
			}
			if strings.Contains(name, "libc.so.6") {
				return "glibc"
			}
		}
	}
	return ""
}

func classifyLibcOutput(output string) string {
	output = strings.ToLower(output)
	if strings.Contains(output, "musl") {
		return "musl"
	}
	if strings.Contains(output, "glibc") || strings.Contains(output, "gnu libc") || strings.Contains(output, "free software foundation") {
		return "glibc"
	}
	return ""
}
