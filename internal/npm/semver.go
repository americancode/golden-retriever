package npm

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type nonRegistryAliasError struct {
	DepName string
}

func (e *nonRegistryAliasError) Error() string {
	return fmt.Sprintf("%s: aliases only work for registry deps", e.DepName)
}

var (
	versionRe         = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([0-9A-Za-z.-]+))?(?:\+([0-9A-Za-z.-]+))?$`)
	partialRe         = regexp.MustCompile(`^v?([0-9xX*]+)(?:\.([0-9xX*]+))?(?:\.([0-9xX*]+))?(?:-([0-9A-Za-z.-]+))?$`)
	hyphenRangeRe     = regexp.MustCompile(`^\s*(\S+)\s+-\s+(\S+)\s*$`)
	comparatorTrimRe  = regexp.MustCompile(`(<=|>=|<|>|=)\s+`)
	rangePrefixTrimRe = regexp.MustCompile(`(\^|~>?)\s+`)
)

func parsePackageSpec(depName, spec string) (string, string, error) {
	spec = strings.TrimSpace(spec)
	if !strings.HasPrefix(strings.ToLower(spec), "npm:") {
		return depName, spec, nil
	}
	rest := spec[len("npm:"):]
	name, wanted, registry, err := parseRegistryPackageArg(rest)
	if err != nil {
		return "", "", err
	}
	if !registry {
		return "", "", &nonRegistryAliasError{DepName: depName}
	}
	if name == "" {
		if registryTagLike(wanted) && !validTagName(wanted) {
			return "", "", &InvalidTagNameError{Name: depName, Spec: spec}
		}
		return "", "", fmt.Errorf("%s: aliases must have a name", depName)
	}
	if strings.HasPrefix(strings.ToLower(wanted), "npm:") {
		return "", "", fmt.Errorf("%s: nested aliases not supported", depName)
	}
	if unsupportedSpecClass(wanted) {
		return "", "", &nonRegistryAliasError{DepName: depName}
	}
	return name, wanted, nil
}

func parseRegistryPackageArg(arg string) (string, string, bool, error) {
	arg = strings.TrimSpace(arg)
	if strings.HasPrefix(strings.ToLower(arg), "npm:") {
		return "", "", false, fmt.Errorf("nested aliases not supported")
	}
	nameEndsAt := strings.Index(arg[1:], "@")
	if nameEndsAt >= 0 {
		nameEndsAt++
	}
	namePart := arg
	if nameEndsAt > 0 {
		namePart = arg[:nameEndsAt]
	}
	if npaURLSpecRe.MatchString(arg) || gitSSHSpecRe.MatchString(arg) {
		return "", "", false, nil
	}
	lower := strings.ToLower(namePart)
	if !strings.HasPrefix(namePart, "@") && (strings.Contains(namePart, "/") || strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tar")) {
		return "", "", false, nil
	}
	if nameEndsAt > 0 {
		wanted := arg[nameEndsAt+1:]
		if wanted == "" {
			wanted = "*"
		}
		return namePart, wanted, true, nil
	}
	if validPackageName(arg) {
		return arg, "*", true, nil
	}
	return "", arg, true, nil
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
	return pickVersionWithOptions(pack, spec, ResolveOptions{})
}

func pickVersionWithOptions(pack *Packument, spec string, opts ResolveOptions) (string, error) {
	spec = strings.TrimSpace(spec)
	defaultTag := opts.DefaultTag
	if defaultTag == "" {
		defaultTag = "latest"
	}
	if spec == "" {
		spec = defaultTag
	}
	if opts.AvoidStrict {
		looseOpts := opts
		looseOpts.AvoidStrict = false
		version, err := pickVersionWithOptions(pack, spec, looseOpts)
		if err != nil || !versionAvoided(version, opts.Avoid) {
			return version, err
		}
		version, err = pickVersionWithOptions(pack, "^"+version, looseOpts)
		if err != nil || !versionAvoided(version, opts.Avoid) {
			return version, err
		}
		version, err = pickVersionWithOptions(pack, "*", looseOpts)
		if err != nil || !versionAvoided(version, opts.Avoid) {
			return version, err
		}
		return "", fmt.Errorf("no avoidable versions for %s", pack.Name)
	}
	if tag, ok := pack.DistTags[spec]; ok {
		if versionBefore(pack, tag, opts.Before) {
			if pack.versionRestricted(tag) || !pack.versionAvailable(tag, opts) {
				return "", fmt.Errorf("version %s@%s is restricted by registry policy", pack.Name, tag)
			}
			return tag, nil
		}
		return pickVersionWithOptions(pack, "<="+tag, opts)
	}
	if parsed := parseVersion(spec); parsed.ok {
		version := parsed.clean()
		if !versionBefore(pack, version, opts.Before) {
			return "", fmt.Errorf("no version of %s satisfies %q before %s", pack.Name, spec, opts.Before.Format(time.RFC3339))
		}
		if pack.versionRestricted(version) || !pack.versionAvailable(version, opts) {
			return "", fmt.Errorf("version %s@%s is restricted by registry policy", pack.Name, version)
		}
		return version, nil
	}

	rangeSpec := spec
	defaultVer := pack.DistTags[defaultTag]
	if defaultVer != "" && satisfies(defaultVer, rangeSpec) {
		if manifest, ok := pack.versionManifest(defaultVer, opts); ok && !pack.versionRestricted(defaultVer) && !pack.versionStaged(defaultVer) && !versionAvoided(defaultVer, opts.Avoid) && versionBefore(pack, defaultVer, opts.Before) && manifest.Deprecated == nil && manifestEngineOK(manifest, opts) {
			return defaultVer, nil
		}
	}

	versions := pack.candidateVersions(opts, true)
	if len(versions) == 0 {
		return "", fmt.Errorf("no versions available for %s", pack.Name)
	}
	sort.Slice(versions, func(i, j int) bool {
		mi, _ := pack.versionManifest(versions[i], opts)
		mj, _ := pack.versionManifest(versions[j], opts)
		avoidI := !versionAvoided(versions[i], opts.Avoid)
		avoidJ := !versionAvoided(versions[j], opts.Avoid)
		if avoidI != avoidJ {
			return avoidI
		}
		restrictedI := !pack.versionRestricted(versions[i])
		restrictedJ := !pack.versionRestricted(versions[j])
		if restrictedI != restrictedJ {
			return restrictedI
		}
		stagedI := !pack.versionStaged(versions[i])
		stagedJ := !pack.versionStaged(versions[j])
		if stagedI != stagedJ {
			return stagedI
		}
		engOKi := manifestEngineOK(mi, opts)
		engOKj := manifestEngineOK(mj, opts)
		notDepri := mi.Deprecated == nil
		notDeprj := mj.Deprecated == nil
		notDeprEngOKi := notDepri && engOKi
		notDeprEngOKj := notDeprj && engOKj
		if notDeprEngOKi != notDeprEngOKj {
			return notDeprEngOKi
		}
		if engOKi != engOKj {
			return engOKi
		}
		if notDepri != notDeprj {
			return notDepri
		}
		return compareVersion(versions[i], versions[j]) > 0
	})
	for _, version := range versions {
		if satisfies(version, spec) {
			if pack.versionRestricted(version) {
				return "", fmt.Errorf("version %s@%s is restricted by registry policy", pack.Name, version)
			}
			return version, nil
		}
	}
	return "", fmt.Errorf("no version of %s satisfies %q", pack.Name, spec)
}

func (pack *Packument) versionAvailable(version string, opts ResolveOptions) bool {
	_, ok := pack.versionManifest(version, opts)
	return ok
}

func (pack *Packument) versionManifest(version string, opts ResolveOptions) (VersionManifest, bool) {
	if pack == nil {
		return VersionManifest{}, false
	}
	if manifest, ok := pack.Versions[version]; ok {
		return manifest, true
	}
	if opts.IncludeStaged {
		if manifest, ok := pack.StagedVersions.Versions[version]; ok {
			return manifest, true
		}
	}
	if manifest, ok := pack.PolicyRestrictions.Versions[version]; ok {
		return manifest, true
	}
	return VersionManifest{}, false
}

func (pack *Packument) versionRestricted(version string) bool {
	if pack == nil || pack.PolicyRestrictions.Versions == nil {
		return false
	}
	_, ok := pack.PolicyRestrictions.Versions[version]
	return ok
}

func (pack *Packument) versionStaged(version string) bool {
	if pack == nil || pack.StagedVersions.Versions == nil {
		return false
	}
	_, ok := pack.StagedVersions.Versions[version]
	return ok
}

func (pack *Packument) candidateVersions(opts ResolveOptions, includeRestricted bool) []string {
	seen := map[string]bool{}
	versions := make([]string, 0, len(pack.Versions)+len(pack.StagedVersions.Versions)+len(pack.PolicyRestrictions.Versions))
	add := func(version string) {
		if !seen[version] && parseVersion(version).ok && versionBefore(pack, version, opts.Before) {
			seen[version] = true
			versions = append(versions, version)
		}
	}
	for version := range pack.Versions {
		add(version)
	}
	if opts.IncludeStaged {
		for version := range pack.StagedVersions.Versions {
			add(version)
		}
	}
	if includeRestricted {
		for version := range pack.PolicyRestrictions.Versions {
			add(version)
		}
	}
	return versions
}

func versionAvoided(version, avoid string) bool {
	return strings.TrimSpace(avoid) != "" && satisfies(version, avoid)
}

func versionBefore(pack *Packument, version string, before time.Time) bool {
	if before.IsZero() || pack == nil || pack.Time == nil {
		return true
	}
	raw := pack.Time[version]
	if raw == "" {
		return true
	}
	published, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return true
	}
	return !published.After(before)
}

func pickVersionSatisfyingAll(pack *Packument, specs []string, opts ResolveOptions) (string, bool) {
	versions := pack.candidateVersions(opts, false)
	sort.Slice(versions, func(i, j int) bool {
		mi, _ := pack.versionManifest(versions[i], opts)
		mj, _ := pack.versionManifest(versions[j], opts)
		avoidI := !versionAvoided(versions[i], opts.Avoid)
		avoidJ := !versionAvoided(versions[j], opts.Avoid)
		if avoidI != avoidJ {
			return avoidI
		}
		stagedI := !pack.versionStaged(versions[i])
		stagedJ := !pack.versionStaged(versions[j])
		if stagedI != stagedJ {
			return stagedI
		}
		engOKi := manifestEngineOK(mi, opts)
		engOKj := manifestEngineOK(mj, opts)
		notDepri := mi.Deprecated == nil
		notDeprj := mj.Deprecated == nil
		notDeprEngOKi := notDepri && engOKi
		notDeprEngOKj := notDeprj && engOKj
		if notDeprEngOKi != notDeprEngOKj {
			return notDeprEngOKi
		}
		if engOKi != engOKj {
			return engOKi
		}
		if notDepri != notDeprj {
			return notDepri
		}
		return compareVersion(versions[i], versions[j]) > 0
	})
	for _, version := range versions {
		matches := true
		for _, spec := range specs {
			if !satisfies(version, spec) {
				matches = false
				break
			}
		}
		if matches {
			return version, true
		}
	}
	return "", false
}

func manifestEngineOK(manifest VersionManifest, opts ResolveOptions) bool {
	ok, _ := engineCompatible(manifest, opts)
	return ok
}

type npmVersion struct {
	major      int
	minor      int
	patch      int
	prerelease []string
	ok         bool
}

func (v npmVersion) clean() string {
	if !v.ok {
		return ""
	}
	out := fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
	if len(v.prerelease) > 0 {
		out += "-" + strings.Join(v.prerelease, ".")
	}
	return out
}

func parseVersion(v string) npmVersion {
	m := versionRe.FindStringSubmatch(v)
	if m == nil {
		return npmVersion{}
	}
	if numericHasLeadingZero(m[1]) || numericHasLeadingZero(m[2]) || numericHasLeadingZero(m[3]) {
		return npmVersion{}
	}
	if !validPrereleaseIdentifiers(m[4]) || !validBuildIdentifiers(m[5]) {
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

func numericHasLeadingZero(s string) bool {
	return len(s) > 1 && strings.HasPrefix(s, "0")
}

func validPrereleaseIdentifiers(pre string) bool {
	if pre == "" {
		return true
	}
	for _, id := range strings.Split(pre, ".") {
		if id == "" {
			return false
		}
		if _, numeric := numericIdentifier(id); numeric && numericHasLeadingZero(id) {
			return false
		}
	}
	return true
}

func validBuildIdentifiers(build string) bool {
	if build == "" {
		return true
	}
	for _, id := range strings.Split(build, ".") {
		if id == "" {
			return false
		}
	}
	return true
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
	v := parseVersion(version)
	if !v.ok {
		return false
	}
	if spec == "" || spec == "*" {
		return len(v.prerelease) == 0
	}
	disjuncts := strings.Split(spec, "||")
	hasEmptyDisjunct := false
	for _, disjunct := range disjuncts {
		disjunct = strings.TrimSpace(disjunct)
		if disjunct == "" {
			hasEmptyDisjunct = true
			continue
		}
		if !validRangeDisjunct(disjunct) {
			return false
		}
	}
	if hasEmptyDisjunct {
		return len(v.prerelease) == 0
	}
	for _, disjunct := range disjuncts {
		disjunct = strings.TrimSpace(disjunct)
		if len(v.prerelease) > 0 && !allowsPrerelease(version, disjunct) {
			continue
		}
		if satisfiesAll(version, disjunct) {
			return true
		}
	}
	return false
}

func validRangeDisjunct(spec string) bool {
	spec = normalizeHyphenRange(spec)
	spec = comparatorTrimRe.ReplaceAllString(spec, "$1")
	spec = rangePrefixTrimRe.ReplaceAllString(spec, "$1")
	parts := strings.Fields(spec)
	if len(parts) == 0 {
		return true
	}
	for _, part := range parts {
		if !validRangeComparator(part) {
			return false
		}
	}
	return true
}

func validRangeComparator(spec string) bool {
	spec = strings.TrimSpace(strings.ReplaceAll(spec, " ", ""))
	if spec == "" || spec == "*" || spec == "x" || spec == "X" {
		return true
	}
	if strings.HasPrefix(spec, "^") {
		return validRangePartial(strings.TrimPrefix(spec, "^"))
	}
	if strings.HasPrefix(spec, "~>") {
		return validRangePartial(strings.TrimPrefix(spec, "~>"))
	}
	if strings.HasPrefix(spec, "~") {
		return validRangePartial(strings.TrimPrefix(spec, "~"))
	}
	for _, op := range []string{">=", "<=", ">", "<", "="} {
		if strings.HasPrefix(spec, op) {
			return validComparatorTarget(strings.TrimSpace(strings.TrimPrefix(spec, op)))
		}
	}
	if partialLooksLikeRange(spec) || strings.ContainsAny(spec, "xX*") {
		return validRangePartial(spec)
	}
	return parseVersion(spec).ok
}

func validComparatorTarget(target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	if strings.ContainsAny(target, "xX*") || partialLooksLikeRange(target) {
		return validRangePartial(target)
	}
	return parseVersion(target).ok
}

func validRangePartial(spec string) bool {
	m := partialRe.FindStringSubmatch(strings.TrimSpace(spec))
	return m != nil && validPartialMatch(m)
}

func rangeIntersects(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return true
	}
	if !validRangeSpec(a) || !validRangeSpec(b) {
		return false
	}
	candidates := append(rangeCandidateVersions(a), rangeCandidateVersions(b)...)
	for _, version := range candidates {
		if satisfies(version, a) && satisfies(version, b) {
			return true
		}
	}
	return false
}

func validRangeSpec(spec string) bool {
	for _, disjunct := range strings.Split(spec, "||") {
		disjunct = strings.TrimSpace(disjunct)
		if disjunct == "" {
			continue
		}
		if !validRangeDisjunct(disjunct) {
			return false
		}
	}
	return true
}

func rangeCandidateVersions(spec string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(version string) {
		if parseVersion(version).ok && !seen[version] {
			seen[version] = true
			out = append(out, version)
		}
	}
	add("0.0.0")
	for _, disjunct := range strings.Split(spec, "||") {
		disjunct = normalizeHyphenRange(strings.TrimSpace(disjunct))
		disjunct = comparatorTrimRe.ReplaceAllString(disjunct, "$1")
		disjunct = rangePrefixTrimRe.ReplaceAllString(disjunct, "$1")
		for _, part := range strings.Fields(disjunct) {
			for _, version := range comparatorCandidateVersions(part) {
				add(version)
			}
		}
	}
	return out
}

func comparatorCandidateVersions(spec string) []string {
	spec = strings.TrimSpace(strings.ReplaceAll(spec, " ", ""))
	if spec == "" || spec == "*" || spec == "x" || spec == "X" {
		return []string{"0.0.0"}
	}
	if strings.HasPrefix(spec, "^") {
		return partialLowerCandidates(strings.TrimPrefix(spec, "^"))
	}
	if strings.HasPrefix(spec, "~>") {
		return partialLowerCandidates(strings.TrimPrefix(spec, "~>"))
	}
	if strings.HasPrefix(spec, "~") {
		return partialLowerCandidates(strings.TrimPrefix(spec, "~"))
	}
	for _, op := range []string{">=", "<=", ">", "<", "="} {
		if strings.HasPrefix(spec, op) {
			target := strings.TrimSpace(strings.TrimPrefix(spec, op))
			if op == ">" {
				return nextComparatorCandidates(target)
			}
			return partialLowerCandidates(target)
		}
	}
	return partialLowerCandidates(spec)
}

func partialLowerCandidates(spec string) []string {
	m := partialRe.FindStringSubmatch(strings.TrimSpace(spec))
	if m == nil || !validPartialMatch(m) {
		if parseVersion(spec).ok {
			return []string{spec}
		}
		return nil
	}
	return []string{versionLowerBound(normalizePartial(spec))}
}

func nextComparatorCandidates(target string) []string {
	candidates := partialLowerCandidates(target)
	out := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		v := parseVersion(candidate)
		if !v.ok {
			continue
		}
		if len(v.prerelease) > 0 {
			out = append(out, fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch))
			continue
		}
		out = append(out, fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch+1))
	}
	return out
}

func satisfiesAll(version, spec string) bool {
	spec = normalizeHyphenRange(spec)
	spec = comparatorTrimRe.ReplaceAllString(spec, "$1")
	spec = rangePrefixTrimRe.ReplaceAllString(spec, "$1")
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
	if strings.HasPrefix(spec, "~>") {
		return satisfiesTilde(version, strings.TrimPrefix(spec, "~>"))
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

func allowsPrerelease(version, spec string) bool {
	v := parseVersion(version)
	if !v.ok || len(v.prerelease) == 0 {
		return true
	}
	spec = normalizeHyphenRange(spec)
	for _, part := range strings.Fields(spec) {
		target := comparatorTarget(part)
		if target == "" {
			continue
		}
		p := parseVersion(target)
		if !p.ok || len(p.prerelease) == 0 {
			continue
		}
		if p.major == v.major && p.minor == v.minor && p.patch == v.patch {
			return true
		}
	}
	return false
}

func comparatorTarget(spec string) string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return ""
	}
	if strings.HasPrefix(spec, "^") || strings.HasPrefix(spec, "~") {
		return strings.TrimSpace(spec[1:])
	}
	for _, op := range []string{">=", "<=", ">", "<", "="} {
		if strings.HasPrefix(spec, op) {
			return strings.TrimSpace(strings.TrimPrefix(spec, op))
		}
	}
	return spec
}

func compareOp(version, op, target string) bool {
	if m := partialRe.FindStringSubmatch(strings.TrimSpace(target)); m != nil && !validPartialMatch(m) {
		return false
	}
	if bounds, ok := partialComparatorBounds(target); ok {
		switch op {
		case ">=":
			return compareVersion(version, bounds.lower) >= 0
		case ">":
			if !bounds.hasUpper {
				return false
			}
			return compareVersion(version, bounds.upper) >= 0
		case "<":
			return compareVersion(version, bounds.lower) < 0
		case "<=":
			if !bounds.hasUpper {
				return true
			}
			return compareVersion(version, bounds.upper) < 0
		default:
			if compareVersion(version, bounds.lower) < 0 {
				return false
			}
			return !bounds.hasUpper || compareVersion(version, bounds.upper) < 0
		}
	}
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

type partialBounds struct {
	lower    string
	upper    string
	hasUpper bool
}

func partialComparatorBounds(target string) (partialBounds, bool) {
	m := partialRe.FindStringSubmatch(strings.TrimSpace(target))
	if m == nil {
		return partialBounds{}, false
	}
	if !validPartialMatch(m) {
		return partialBounds{}, false
	}
	if m[2] != "" && m[3] != "" && !isWild(m[1]) && !isWild(m[2]) && !isWild(m[3]) {
		return partialBounds{}, false
	}
	if isWild(m[1]) {
		return partialBounds{lower: "0.0.0"}, true
	}
	major := atoi(m[1])
	if m[2] == "" || isWild(m[2]) {
		return partialBounds{
			lower:    fmt.Sprintf("%d.0.0", major),
			upper:    fmt.Sprintf("%d.0.0", major+1),
			hasUpper: true,
		}, true
	}
	minor := atoi(m[2])
	if m[3] == "" || isWild(m[3]) {
		return partialBounds{
			lower:    fmt.Sprintf("%d.%d.0", major, minor),
			upper:    fmt.Sprintf("%d.%d.0", major, minor+1),
			hasUpper: true,
		}, true
	}
	return partialBounds{}, false
}

func satisfiesCaret(version, base string) bool {
	v := parseVersion(version)
	b := normalizePartial(base)
	if !v.ok || !b.ok {
		return false
	}
	if compareVersion(version, versionLowerBound(b)) < 0 {
		return false
	}
	upper, ok := caretUpperBound(base)
	if !ok {
		return true
	}
	return compareVersion(version, upper) < 0
}

func satisfiesTilde(version, base string) bool {
	v := parseVersion(version)
	b := normalizePartial(base)
	if !v.ok || !b.ok {
		return false
	}
	if compareVersion(version, versionLowerBound(b)) < 0 {
		return false
	}
	upper, ok := tildeUpperBound(base)
	if !ok {
		return true
	}
	return compareVersion(version, upper) < 0
}

func versionLowerBound(v npmVersion) string {
	base := fmt.Sprintf("%d.%d.%d", v.major, v.minor, v.patch)
	if len(v.prerelease) > 0 {
		return base + "-" + strings.Join(v.prerelease, ".")
	}
	return base
}

func satisfiesWildcard(version, spec string) bool {
	v := parseVersion(version)
	if !v.ok {
		return false
	}
	if m := partialRe.FindStringSubmatch(strings.TrimSpace(spec)); m != nil && !validPartialMatch(m) {
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
	if !validPartialMatch(m) {
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

func caretUpperBound(spec string) (string, bool) {
	m := partialRe.FindStringSubmatch(strings.TrimSpace(spec))
	if m == nil {
		return "", false
	}
	if !validPartialMatch(m) {
		return "", false
	}
	if isWild(m[1]) {
		return "", false
	}
	major := atoi(m[1])
	if major > 0 {
		return fmt.Sprintf("%d.0.0", major+1), true
	}
	if m[2] == "" || isWild(m[2]) {
		return "1.0.0", true
	}
	minor := atoi(m[2])
	if minor > 0 {
		return fmt.Sprintf("0.%d.0", minor+1), true
	}
	if m[3] == "" || isWild(m[3]) {
		return "0.1.0", true
	}
	return fmt.Sprintf("0.0.%d", atoi(m[3])+1), true
}

func tildeUpperBound(spec string) (string, bool) {
	m := partialRe.FindStringSubmatch(strings.TrimSpace(spec))
	if m == nil {
		return "", false
	}
	if !validPartialMatch(m) {
		return "", false
	}
	if isWild(m[1]) {
		return "", false
	}
	major := atoi(m[1])
	if m[2] == "" || isWild(m[2]) {
		return fmt.Sprintf("%d.0.0", major+1), true
	}
	return fmt.Sprintf("%d.%d.0", major, atoi(m[2])+1), true
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
	return versionLowerBound(v)
}

func upperHyphenBound(spec string) (string, bool) {
	m := partialRe.FindStringSubmatch(strings.TrimSpace(spec))
	if m == nil {
		return spec, true
	}
	if !validPartialMatch(m) {
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
	if !validPartialMatch(m) {
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
	if m := partialRe.FindStringSubmatch(strings.TrimSpace(target)); m != nil && !validPartialMatch(m) {
		return target
	}
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

func validPartialMatch(m []string) bool {
	if len(m) < 5 {
		return false
	}
	for _, id := range m[1:4] {
		if id == "" || isWild(id) {
			continue
		}
		if numericHasLeadingZero(id) {
			return false
		}
	}
	return validPrereleaseIdentifiers(m[4])
}
