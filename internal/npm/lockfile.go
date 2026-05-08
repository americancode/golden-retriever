package npm

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
)

type lockfile struct {
	Name            string                 `json:"name"`
	LockfileVersion int                    `json:"lockfileVersion"`
	Packages        map[string]lockPackage `json:"packages"`
	Dependencies    map[string]lockPackage `json:"dependencies"`
}

type yarnResolvedPackage struct {
	Name      string
	Version   string
	Resolved  string
	Integrity string
}

type lockPackage struct {
	Name               string                 `json:"name"`
	Version            string                 `json:"version"`
	From               string                 `json:"from"`
	Resolved           string                 `json:"resolved"`
	Integrity          string                 `json:"integrity"`
	Dependencies       map[string]string      `json:"dependencies"`
	NestedDependencies map[string]lockPackage `json:"-"`
	Requires           map[string]string      `json:"requires"`
	Dev                bool                   `json:"dev"`
	Optional           bool                   `json:"optional"`
	InBundle           bool                   `json:"inBundle"`
	Bundled            bool                   `json:"bundled"`
	Link               bool                   `json:"link"`
	Extra              map[string]interface{} `json:"-"`
}

func (p *lockPackage) UnmarshalJSON(data []byte) error {
	var raw struct {
		Name         string            `json:"name"`
		Version      string            `json:"version"`
		From         string            `json:"from"`
		Resolved     string            `json:"resolved"`
		Integrity    string            `json:"integrity"`
		Dependencies json.RawMessage   `json:"dependencies"`
		Requires     map[string]string `json:"requires"`
		Dev          bool              `json:"dev"`
		Optional     bool              `json:"optional"`
		InBundle     bool              `json:"inBundle"`
		Bundled      bool              `json:"bundled"`
		Link         bool              `json:"link"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.Name = raw.Name
	p.Version = raw.Version
	p.From = raw.From
	p.Resolved = raw.Resolved
	p.Integrity = raw.Integrity
	p.Requires = raw.Requires
	p.Dev = raw.Dev
	p.Optional = raw.Optional
	p.InBundle = raw.InBundle
	p.Bundled = raw.Bundled
	p.Link = raw.Link
	if len(raw.Dependencies) > 0 && string(raw.Dependencies) != "null" {
		var specs map[string]string
		if err := json.Unmarshal(raw.Dependencies, &specs); err == nil {
			p.Dependencies = specs
		} else {
			var nested map[string]lockPackage
			if nestedErr := json.Unmarshal(raw.Dependencies, &nested); nestedErr != nil {
				return err
			}
			p.NestedDependencies = nested
		}
	}
	return nil
}

func LoadLockfile(path string) (*Graph, error) {
	return loadLockfile(path, "")
}

func loadLockfile(path, yarnLockPath string) (*Graph, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lock lockfile
	if err := json.Unmarshal(data, &lock); err != nil {
		return nil, err
	}
	yarnResolved := map[string]yarnResolvedPackage{}
	if yarnLockPath != "" {
		if parsed, err := parseYarnLock(yarnLockPath); err == nil {
			yarnResolved = parsed
		}
	}

	g := NewGraph()
	if len(lock.Packages) > 0 {
		for loc, pkg := range lock.Packages {
			if loc == "" {
				continue
			}
			name := pkg.Name
			if name == "" {
				name = nameFromNodeModulesPath(loc)
			}
			version := effectiveLockVersion(pkg)
			selectedYarn := yarnResolvedPackage{}
			if version == "" {
				if yarnPkg, ok := pickYarnResolvedPackage(name, pkg, yarnResolved); ok {
					version = yarnPkg.Version
					selectedYarn = yarnPkg
				}
			}
			if version == "" {
				continue
			}
			if skipLockPackageWithVersion(pkg, version) {
				continue
			}
			resolved := normalizeLockResolved(name, version, pkg.Resolved)
			integrity := pkg.Integrity
			if selectedYarn.Name == "" {
				selectedYarn = yarnResolved[name+"@"+version]
			}
			if selectedYarn.Name != "" {
				if pkg.Resolved == "" && selectedYarn.Resolved != "" {
					resolved = normalizeLockResolved(name, version, selectedYarn.Resolved)
				}
				if integrity == "" && selectedYarn.Integrity != "" {
					integrity = selectedYarn.Integrity
				}
			}
			g.Add(Package{Name: name, Version: version, Tarball: resolved, Integrity: integrity})
		}
	}

	for name, pkg := range lock.Dependencies {
		walkLockDependency(g, name, pkg, yarnResolved)
	}
	return g, nil
}

func walkLockDependency(g *Graph, name string, pkg lockPackage, yarnResolved map[string]yarnResolvedPackage) {
	version := effectiveLockVersion(pkg)
	selectedYarn := yarnResolvedPackage{}
	if version == "" {
		if yarnPkg, ok := pickYarnResolvedPackage(name, pkg, yarnResolved); ok {
			version = yarnPkg.Version
			selectedYarn = yarnPkg
		}
	}
	if version != "" {
		if skipLockPackageWithVersion(pkg, version) {
			return
		}
		resolved := normalizeLockResolved(name, version, pkg.Resolved)
		integrity := pkg.Integrity
		if selectedYarn.Name == "" {
			selectedYarn = yarnResolved[name+"@"+version]
		}
		if selectedYarn.Name != "" {
			if pkg.Resolved == "" && selectedYarn.Resolved != "" {
				resolved = normalizeLockResolved(name, version, selectedYarn.Resolved)
			}
			if integrity == "" && selectedYarn.Integrity != "" {
				integrity = selectedYarn.Integrity
			}
		}
		g.Add(Package{Name: name, Version: version, Tarball: resolved, Integrity: integrity})
	}
	for childName, child := range pkg.NestedDependencies {
		walkLockDependency(g, childName, child, yarnResolved)
	}
}

func skipLockPackage(pkg lockPackage) bool {
	version := effectiveLockVersion(pkg)
	return skipLockPackageWithVersion(pkg, version)
}

func skipLockPackageWithVersion(pkg lockPackage, version string) bool {
	if version == "" || pkg.InBundle || pkg.Bundled || pkg.Link {
		return true
	}
	if !parseVersion(version).ok {
		return true
	}
	return localLockResolved(pkg.Resolved)
}

var semverInTextRe = regexp.MustCompile(`([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?)`)

func effectiveLockVersion(pkg lockPackage) string {
	if strings.TrimSpace(pkg.Version) != "" {
		return strings.TrimSpace(pkg.Version)
	}
	from := strings.TrimSpace(pkg.From)
	if from == "" {
		from = strings.TrimSpace(pkg.Resolved)
		if from == "" {
			return ""
		}
	}
	if _, wanted := splitNameSpec(from); wanted != "" && !parseVersion(wanted).ok {
		return ""
	}
	matches := semverInTextRe.FindAllString(from, -1)
	for _, candidate := range matches {
		if parseVersion(candidate).ok {
			return candidate
		}
	}
	return ""
}

func pickYarnResolvedPackage(name string, pkg lockPackage, yarnResolved map[string]yarnResolvedPackage) (yarnResolvedPackage, bool) {
	candidates := []yarnResolvedPackage{}
	prefix := name + "@"
	for key, candidate := range yarnResolved {
		if strings.HasPrefix(key, prefix) {
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 0 {
		return yarnResolvedPackage{}, false
	}
	if len(candidates) == 1 {
		return candidates[0], true
	}
	if from := strings.TrimSpace(pkg.From); from != "" {
		_, wanted := splitNameSpec(from)
		wanted = strings.TrimSpace(wanted)
		if wanted != "" {
			best := yarnResolvedPackage{}
			for _, candidate := range candidates {
				if !satisfies(candidate.Version, wanted) {
					continue
				}
				if best.Name == "" || compareVersion(candidate.Version, best.Version) > 0 {
					best = candidate
				}
			}
			if best.Name != "" {
				return best, true
			}
		}
	}
	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if compareVersion(candidate.Version, best.Version) > 0 {
			best = candidate
		}
	}
	return best, true
}

func normalizeLockResolved(name, version, resolved string) string {
	resolved = strings.TrimSpace(resolved)
	if resolved == "" {
		return defaultTarballURL(name, version)
	}
	lower := strings.ToLower(resolved)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return resolved
	}
	if strings.HasPrefix(resolved, "//") {
		return "https:" + resolved
	}
	if strings.HasPrefix(resolved, "/") {
		return strings.TrimSuffix(DefaultRegistry, "/") + resolved
	}
	if strings.HasPrefix(lower, "registry.npmjs.org/") {
		return "https://" + resolved
	}
	return resolved
}

func parseYarnLock(path string) (map[string]yarnResolvedPackage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	out := map[string]yarnResolvedPackage{}
	for i := 0; i < len(lines); {
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "__metadata:") {
			i++
			continue
		}
		if !strings.HasSuffix(line, ":") {
			i++
			continue
		}
		header := strings.TrimSuffix(line, ":")
		selectors := strings.Split(header, ",")
		var version, resolved, integrity string
		i++
		for i < len(lines) {
			raw := lines[i]
			trim := strings.TrimSpace(raw)
			if trim == "" {
				i++
				break
			}
			if !strings.HasPrefix(raw, " ") && strings.HasSuffix(strings.TrimSpace(raw), ":") {
				break
			}
			if strings.HasPrefix(trim, "version ") {
				version = trimQuoted(strings.TrimPrefix(trim, "version "))
			} else if strings.HasPrefix(trim, "resolved ") {
				resolved = strings.Split(trimQuoted(strings.TrimPrefix(trim, "resolved ")), "#")[0]
			} else if strings.HasPrefix(trim, "integrity ") {
				integrity = trimQuoted(strings.TrimPrefix(trim, "integrity "))
			}
			i++
		}
		if version == "" {
			continue
		}
		names := []string{}
		for _, selector := range selectors {
			sel := trimQuoted(strings.TrimSpace(selector))
			if sel == "" {
				continue
			}
			name, _ := splitNameSpec(sel)
			if name == "" {
				continue
			}
			if !stringSliceContains(names, name) {
				names = append(names, name)
			}
		}
		for _, name := range names {
			key := name + "@" + version
			if existing, ok := out[key]; ok && existing.Resolved != "" && existing.Integrity != "" {
				continue
			}
			out[key] = yarnResolvedPackage{Name: name, Version: version, Resolved: resolved, Integrity: integrity}
		}
	}
	return out, nil
}

func trimQuoted(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, `"`)
	value = strings.TrimSuffix(value, `"`)
	return strings.TrimSpace(value)
}

func stringSliceContains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}

func localLockResolved(resolved string) bool {
	resolved = strings.TrimSpace(resolved)
	if resolved == "" {
		return false
	}
	lower := strings.ToLower(resolved)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return false
	}
	if strings.HasPrefix(resolved, "//") {
		return false
	}
	if strings.HasPrefix(lower, "file:") || strings.HasPrefix(lower, "link:") ||
		strings.HasPrefix(lower, "git:") || strings.HasPrefix(lower, "git+") ||
		strings.HasPrefix(lower, "github:") || strings.HasPrefix(lower, "gitlab:") ||
		strings.HasPrefix(lower, "bitbucket:") || strings.HasPrefix(lower, "gist:") ||
		strings.HasPrefix(lower, "ssh:") || strings.HasPrefix(lower, "svn:") {
		return true
	}
	if strings.HasPrefix(resolved, "/") {
		if (strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tar")) &&
			strings.Contains(resolved, "/-/") {
			return false
		}
		return true
	}
	return strings.HasPrefix(resolved, ".") || strings.HasPrefix(resolved, "/") ||
		strings.HasPrefix(resolved, "~") || windowsDriveSpecRe.MatchString(resolved)
}

func defaultTarballURL(name, version string) string {
	if name == "" || version == "" {
		return ""
	}
	baseName := name
	if strings.HasPrefix(name, "@") {
		if _, after, ok := strings.Cut(name, "/"); ok {
			baseName = after
		}
	}
	return DefaultRegistry + "/" + name + "/-/" + baseName + "-" + version + ".tgz"
}

func nameFromNodeModulesPath(path string) string {
	parts := strings.Split(path, "node_modules/")
	if len(parts) == 0 {
		return path
	}
	return parts[len(parts)-1]
}
