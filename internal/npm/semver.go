package npm

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var (
	versionRe     = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?$`)
	partialRe     = regexp.MustCompile(`^v?([0-9xX*]+)(?:\.([0-9xX*]+))?(?:\.([0-9xX*]+))?(?:-([0-9A-Za-z.-]+))?$`)
	hyphenRangeRe = regexp.MustCompile(`^\s*(\S+)\s+-\s+(\S+)\s*$`)
)

func parsePackageSpec(depName, spec string) (string, string, error) {
	spec = strings.TrimSpace(spec)
	if !strings.HasPrefix(spec, "npm:") {
		return depName, spec, nil
	}
	rest := strings.TrimPrefix(spec, "npm:")
	if rest == "" {
		return "", "", fmt.Errorf("%s: empty npm alias spec", depName)
	}
	name, wanted := splitNameSpec(rest)
	if name == "" {
		return "", "", fmt.Errorf("%s: invalid npm alias spec %q", depName, spec)
	}
	if wanted == "" {
		wanted = "latest"
	}
	return name, wanted, nil
}

func splitNameSpec(spec string) (string, string) {
	if strings.HasPrefix(spec, "@") {
		firstSlash := strings.Index(spec, "/")
		if firstSlash == -1 {
			return spec, ""
		}
		rest := spec[firstSlash+1:]
		at := strings.Index(rest, "@")
		if at == -1 {
			return spec, ""
		}
		nameEnd := firstSlash + 1 + at
		return spec[:nameEnd], spec[nameEnd+1:]
	}
	at := strings.LastIndex(spec, "@")
	if at <= 0 {
		return spec, ""
	}
	return spec[:at], spec[at+1:]
}

func pickVersion(pack *Packument, spec string) (string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == "*" {
		spec = "latest"
	}
	if tag, ok := pack.DistTags[spec]; ok {
		return tag, nil
	}
	if _, ok := pack.Versions[spec]; ok {
		return spec, nil
	}

	rangeSpec := spec
	defaultVer := pack.DistTags["latest"]
	if defaultVer != "" && satisfies(defaultVer, rangeSpec) {
		if manifest, ok := pack.Versions[defaultVer]; ok && manifest.Deprecated == nil {
			return defaultVer, nil
		}
	}

	versions := make([]string, 0, len(pack.Versions))
	for version, manifest := range pack.Versions {
		if parseVersion(version).ok {
			versions = append(versions, version)
			_ = manifest
		}
	}
	sort.Slice(versions, func(i, j int) bool {
		mi := pack.Versions[versions[i]]
		mj := pack.Versions[versions[j]]
		notDepri := mi.Deprecated == nil
		notDeprj := mj.Deprecated == nil
		if notDepri != notDeprj {
			return notDepri
		}
		return compareVersion(versions[i], versions[j]) > 0
	})
	for _, version := range versions {
		if satisfies(version, spec) {
			return version, nil
		}
	}
	return "", fmt.Errorf("no version of %s satisfies %q", pack.Name, spec)
}

type npmVersion struct {
	major      int
	minor      int
	patch      int
	prerelease []string
	ok         bool
}

func parseVersion(v string) npmVersion {
	m := versionRe.FindStringSubmatch(v)
	if m == nil {
		return npmVersion{}
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch, _ := strconv.Atoi(m[3])
	var pre []string
	if m[4] != "" {
		pre = strings.Split(m[4], ".")
	}
	return npmVersion{major: major, minor: minor, patch: patch, prerelease: pre, ok: true}
}

func compareVersion(a, b string) int {
	av := parseVersion(a)
	bv := parseVersion(b)
	if !av.ok || !bv.ok {
		return strings.Compare(a, b)
	}
	if av.major != bv.major {
		return av.major - bv.major
	}
	if av.minor != bv.minor {
		return av.minor - bv.minor
	}
	if av.patch != bv.patch {
		return av.patch - bv.patch
	}
	return comparePrerelease(av.prerelease, bv.prerelease)
}

func comparePrerelease(a, b []string) int {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	if len(a) == 0 {
		return 1
	}
	if len(b) == 0 {
		return -1
	}
	max := len(a)
	if len(b) > max {
		max = len(b)
	}
	for i := 0; i < max; i++ {
		if i >= len(a) {
			return -1
		}
		if i >= len(b) {
			return 1
		}
		ai, aNum := numericIdentifier(a[i])
		bi, bNum := numericIdentifier(b[i])
		if aNum && bNum && ai != bi {
			return ai - bi
		}
		if aNum != bNum {
			if aNum {
				return -1
			}
			return 1
		}
		if a[i] != b[i] {
			return strings.Compare(a[i], b[i])
		}
	}
	return 0
}

func numericIdentifier(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	i, _ := strconv.Atoi(s)
	return i, true
}

func satisfies(version, spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == "*" || spec == "latest" {
		return len(parseVersion(version).prerelease) == 0
	}
	if len(parseVersion(version).prerelease) > 0 && !strings.Contains(spec, "-") {
		return false
	}
	for _, disjunct := range strings.Split(spec, "||") {
		if satisfiesAll(version, strings.TrimSpace(disjunct)) {
			return true
		}
	}
	return false
}

func satisfiesAll(version, spec string) bool {
	spec = normalizeHyphenRange(spec)
	parts := strings.Fields(spec)
	if len(parts) == 0 {
		return true
	}
	for _, part := range parts {
		if !satisfiesOne(version, part) {
			return false
		}
	}
	return true
}

func satisfiesOne(version, spec string) bool {
	spec = strings.TrimSpace(spec)
	if spec == "" || spec == "*" || spec == "x" || spec == "X" {
		return true
	}
	spec = strings.ReplaceAll(spec, " ", "")
	if strings.HasPrefix(spec, "^") {
		return satisfiesCaret(version, strings.TrimPrefix(spec, "^"))
	}
	if strings.HasPrefix(spec, "~") {
		return satisfiesTilde(version, strings.TrimPrefix(spec, "~"))
	}
	for _, op := range []string{">=", "<=", ">", "<", "="} {
		if strings.HasPrefix(spec, op) {
			return compareOp(version, op, strings.TrimSpace(strings.TrimPrefix(spec, op)))
		}
	}
	if partialLooksLikeRange(spec) {
		return satisfiesPartial(version, spec)
	}
	if strings.ContainsAny(spec, "xX*") {
		return satisfiesWildcard(version, spec)
	}
	return compareVersion(version, spec) == 0
}

func compareOp(version, op, target string) bool {
	target = completeComparatorTarget(target, op)
	cmp := compareVersion(version, target)
	switch op {
	case ">=":
		return cmp >= 0
	case "<=":
		return cmp <= 0
	case ">":
		return cmp > 0
	case "<":
		return cmp < 0
	default:
		return cmp == 0
	}
}

func satisfiesCaret(version, base string) bool {
	v := parseVersion(version)
	b := normalizePartial(base)
	if !v.ok || !b.ok {
		return false
	}
	if compareVersion(version, fmt.Sprintf("%d.%d.%d", b.major, b.minor, b.patch)) < 0 {
		return false
	}
	if b.major > 0 {
		return v.major == b.major
	}
	if b.minor > 0 {
		return v.major == 0 && v.minor == b.minor
	}
	return v.major == 0 && v.minor == 0 && v.patch == b.patch
}

func satisfiesTilde(version, base string) bool {
	v := parseVersion(version)
	b := normalizePartial(base)
	if !v.ok || !b.ok {
		return false
	}
	if compareVersion(version, fmt.Sprintf("%d.%d.%d", b.major, b.minor, b.patch)) < 0 {
		return false
	}
	parts := strings.Split(base, ".")
	if len(parts) == 1 {
		return v.major == b.major
	}
	return v.major == b.major && v.minor == b.minor
}

func satisfiesWildcard(version, spec string) bool {
	v := parseVersion(version)
	if !v.ok {
		return false
	}
	parts := strings.Split(spec, ".")
	if len(parts) > 0 && isWild(parts[0]) {
		return true
	}
	if len(parts) > 0 && atoi(parts[0]) != v.major {
		return false
	}
	if len(parts) > 1 && !isWild(parts[1]) && atoi(parts[1]) != v.minor {
		return false
	}
	if len(parts) > 2 && !isWild(parts[2]) && atoi(parts[2]) != v.patch {
		return false
	}
	return true
}

func normalizePartial(spec string) npmVersion {
	m := partialRe.FindStringSubmatch(strings.TrimSpace(spec))
	if m == nil {
		return npmVersion{}
	}
	v := npmVersion{major: atoi(m[1]), ok: true}
	if m[2] != "" && !isWild(m[2]) {
		v.minor = atoi(m[2])
	}
	if m[3] != "" && !isWild(m[3]) {
		v.patch = atoi(m[3])
	}
	if m[4] != "" {
		v.prerelease = strings.Split(m[4], ".")
	}
	return v
}

func normalizeHyphenRange(spec string) string {
	m := hyphenRangeRe.FindStringSubmatch(spec)
	if m == nil {
		return spec
	}
	upper, inclusive := upperHyphenBound(m[2])
	if inclusive {
		return ">=" + lowerHyphenBound(m[1]) + " <=" + upper
	}
	return ">=" + lowerHyphenBound(m[1]) + " <" + upper
}

func lowerHyphenBound(spec string) string {
	v := normalizePartial(spec)
	if !v.ok {
		return spec
	}
	return fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
}

func upperHyphenBound(spec string) (string, bool) {
	m := partialRe.FindStringSubmatch(strings.TrimSpace(spec))
	if m == nil {
		return spec, true
	}
	if m[2] == "" || isWild(m[2]) {
		return fmt.Sprintf("%d.0.0", atoi(m[1])+1), false
	}
	if m[3] == "" || isWild(m[3]) {
		return fmt.Sprintf("%s.%d.0", m[1], atoi(m[2])+1), false
	}
	return spec, true
}

func partialLooksLikeRange(spec string) bool {
	m := partialRe.FindStringSubmatch(spec)
	if m == nil {
		return false
	}
	return m[2] == "" || m[3] == "" || isWild(m[1]) || isWild(m[2]) || isWild(m[3])
}

func satisfiesPartial(version, spec string) bool {
	if strings.ContainsAny(spec, "xX*") {
		return satisfiesWildcard(version, spec)
	}
	v := parseVersion(version)
	p := normalizePartial(spec)
	if !v.ok || !p.ok {
		return false
	}
	parts := strings.Split(spec, ".")
	if len(parts) == 1 {
		return v.major == p.major
	}
	return v.major == p.major && v.minor == p.minor
}

func completeComparatorTarget(target, op string) string {
	target = strings.TrimSpace(target)
	if strings.ContainsAny(target, "xX*") {
		return wildcardComparatorTarget(target, op)
	}
	m := partialRe.FindStringSubmatch(target)
	if m == nil {
		return target
	}
	if m[2] == "" {
		if op == "<" || op == "<=" {
			return fmt.Sprintf("%s.0.0", m[1])
		}
		return fmt.Sprintf("%s.0.0", m[1])
	}
	if m[3] == "" {
		if op == "<" || op == "<=" {
			return fmt.Sprintf("%s.%s.0", m[1], m[2])
		}
		return fmt.Sprintf("%s.%s.0", m[1], m[2])
	}
	return target
}

func wildcardComparatorTarget(target, op string) string {
	parts := strings.Split(target, ".")
	for len(parts) < 3 {
		parts = append(parts, "x")
	}
	major := parts[0]
	minor := parts[1]
	patch := parts[2]
	if isWild(major) {
		return "0.0.0"
	}
	if isWild(minor) {
		if op == "<" || op == "<=" {
			return fmt.Sprintf("%d.0.0", atoi(major)+1)
		}
		return fmt.Sprintf("%s.0.0", major)
	}
	if isWild(patch) {
		if op == "<" || op == "<=" {
			return fmt.Sprintf("%s.%d.0", major, atoi(minor)+1)
		}
		return fmt.Sprintf("%s.%s.0", major, minor)
	}
	return target
}

func isWild(s string) bool {
	s = strings.TrimSpace(s)
	return s == "*" || s == "x" || s == "X"
}

func atoi(s string) int {
	i, _ := strconv.Atoi(strings.TrimSpace(s))
	return i
}
