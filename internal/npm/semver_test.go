package npm

import (
	"strings"
	"testing"
	"time"
)

func TestPickVersionRanges(t *testing.T) {
	pack := &Packument{
		Name:     "demo",
		DistTags: map[string]string{"latest": "2.1.0"},
		Versions: map[string]VersionManifest{
			"1.0.0":        {},
			"1.2.3":        {},
			"1.9.0":        {},
			"2.0.0":        {},
			"2.1.0":        {},
			"3.0.0-beta.1": {},
		},
	}

	tests := map[string]string{
		"latest":       "2.1.0",
		"^1.2.0":       "1.9.0",
		"~1.2.0":       "1.2.3",
		">=1.0.0 <2":   "1.9.0",
		"1.x":          "1.9.0",
		"1":            "1.9.0",
		"1.2":          "1.2.3",
		"1.0.0 - 1.2":  "1.2.3",
		"2.0.0":        "2.0.0",
		"^1.2 || >=2":  "2.1.0",
		">=3.0.0-beta": "3.0.0-beta.1",
	}

	for spec, want := range tests {
		got, err := pickVersion(pack, spec)
		if err != nil {
			t.Fatalf("%s: %v", spec, err)
		}
		if got != want {
			t.Fatalf("%s: got %s want %s", spec, got, want)
		}
	}
}

func TestPickVersionResolutionFixtures(t *testing.T) {
	pack := &Packument{
		Name: "fixture",
		DistTags: map[string]string{
			"latest": "2.1.0",
			"next":   "3.0.0-beta.1",
		},
		Versions: map[string]VersionManifest{
			"1.0.0":        {},
			"1.2.3":        {},
			"1.9.0":        {},
			"2.0.0":        {},
			"2.1.0":        {},
			"3.0.0-beta.1": {},
		},
	}

	tests := []struct {
		name string
		spec string
		want string
	}{
		{name: "dist-tag", spec: "latest", want: "2.1.0"},
		{name: "exact-version", spec: "2.0.0", want: "2.0.0"},
		{name: "range", spec: "^1.2.0", want: "1.9.0"},
		{name: "prerelease", spec: ">=3.0.0-beta <4", want: "3.0.0-beta.1"},
		{name: "hyphen-range", spec: "1.0.0 - 1.2.3", want: "1.2.3"},
		{name: "or-range", spec: "^1.2.0 || >=2.0.0", want: "2.1.0"},
		{name: "comparator-whitespace", spec: " < 2.0.0  ||  >= 2.1.0 ", want: "2.1.0"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := pickVersion(pack, tc.spec)
			if err != nil {
				t.Fatalf("pickVersion(%q) err = %v", tc.spec, err)
			}
			if got != tc.want {
				t.Fatalf("pickVersion(%q) = %s want %s", tc.spec, got, tc.want)
			}
		})
	}

	t.Run("conflicting-ranges", func(t *testing.T) {
		if got, ok := pickVersionSatisfyingAll(pack, []string{">=1.0.0 <2.0.0", ">=2.0.0"}, ResolveOptions{}); ok {
			t.Fatalf("pickVersionSatisfyingAll(conflicting) = %s, want no match", got)
		}
	})
}

func TestParsePackageSpecMatchesNPARegistryAliases(t *testing.T) {
	tests := []struct {
		spec       string
		wantName   string
		wantWanted string
	}{
		{spec: "npm:left-pad@1.0.0", wantName: "left-pad", wantWanted: "1.0.0"},
		{spec: "NPM:left-pad@1.0.0", wantName: "left-pad", wantWanted: "1.0.0"},
		{spec: "npm:left-pad", wantName: "left-pad", wantWanted: "*"},
		{spec: "npm:left-pad@", wantName: "left-pad", wantWanted: "*"},
		{spec: "npm:@scope/pkg", wantName: "@scope/pkg", wantWanted: "*"},
		{spec: "npm:@scope/pkg@^1", wantName: "@scope/pkg", wantWanted: "^1"},
		{spec: "npm:CAPS@1", wantName: "CAPS", wantWanted: "1"},
		{spec: "npm:@scope/_private@1", wantName: "@scope/_private", wantWanted: "1"},
		{spec: "npm:@scope/-dash@1", wantName: "@scope/-dash", wantWanted: "1"},
	}
	for _, tc := range tests {
		t.Run(tc.spec, func(t *testing.T) {
			name, wanted, err := parsePackageSpec("alias", tc.spec)
			if err != nil {
				t.Fatal(err)
			}
			if name != tc.wantName || wanted != tc.wantWanted {
				t.Fatalf("parsePackageSpec(%q) = %q, %q; want %q, %q", tc.spec, name, wanted, tc.wantName, tc.wantWanted)
			}
		})
	}
}

func TestParsePackageSpecRejectsNonRegistryAliases(t *testing.T) {
	for _, spec := range []string{
		"npm:file:../local",
		"npm:https://registry.npmjs.org/pkg/-/pkg-1.0.0.tgz",
		"npm:foo/bar",
		"npm:foo@file:../local",
		"npm:foo:bar",
	} {
		t.Run(spec, func(t *testing.T) {
			if _, _, err := parsePackageSpec("alias", spec); err == nil {
				t.Fatalf("parsePackageSpec(%q) = nil, want error", spec)
			}
		})
	}
}

func TestParsePackageSpecAliasWhitespaceParity(t *testing.T) {
	tests := []struct {
		spec string
		want string
	}{
		{spec: "npm: foo@^1.0.0", want: `invalid package name " foo"`},
		{spec: "npm:foo @^1.0.0", want: `invalid package name "foo "`},
		{spec: "npm:foo ", want: "aliases must have a name"},
	}
	for _, tc := range tests {
		t.Run(tc.spec, func(t *testing.T) {
			err := validateDependencySpec("alias", tc.spec, EdgeProd)
			if err == nil {
				t.Fatalf("validateDependencySpec(%q) = nil, want error", tc.spec)
			}
			if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) {
				t.Fatalf("validateDependencySpec(%q) err = %v, want substring %q", tc.spec, err, tc.want)
			}
		})
	}
}

func TestPickVersionPrefersLatestWhenItSatisfies(t *testing.T) {
	pack := &Packument{
		Name:     "demo",
		DistTags: map[string]string{"latest": "1.2.0"},
		Versions: map[string]VersionManifest{
			"1.2.0": {},
			"1.3.0": {},
		},
	}

	got, err := pickVersion(pack, "^1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.2.0" {
		t.Fatalf("got %s want latest-compatible 1.2.0", got)
	}
}

func TestPickVersionHonorsDefaultTag(t *testing.T) {
	pack := &Packument{
		Name:     "demo",
		DistTags: map[string]string{"latest": "2.0.0", "beta": "3.0.0-beta.1"},
		Versions: map[string]VersionManifest{
			"2.0.0":        {},
			"3.0.0-beta.1": {},
		},
	}

	got, err := pickVersionWithOptions(pack, "", ResolveOptions{DefaultTag: "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "3.0.0-beta.1" {
		t.Fatalf("got %s want beta dist-tag", got)
	}
}

func TestPickVersionAvoidsDeprecatedLatest(t *testing.T) {
	pack := &Packument{
		Name:     "demo",
		DistTags: map[string]string{"latest": "1.2.0"},
		Versions: map[string]VersionManifest{
			"1.1.0": {},
			"1.2.0": {Deprecated: "bad"},
		},
	}

	got, err := pickVersion(pack, "^1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.1.0" {
		t.Fatalf("got %s want non-deprecated 1.1.0", got)
	}
}

func TestPickVersionHonorsStagedVersions(t *testing.T) {
	pack := &Packument{
		Name:     "demo",
		DistTags: map[string]string{"latest": "1.1.0"},
		Versions: map[string]VersionManifest{
			"1.0.0": {Version: "1.0.0"},
		},
	}
	pack.StagedVersions.Versions = map[string]VersionManifest{
		"1.1.0": {Version: "1.1.0"},
	}

	got, err := pickVersionWithOptions(pack, "^1.0.0", ResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.0.0" {
		t.Fatalf("without include-staged got %s want normal 1.0.0", got)
	}
	if _, err := pickVersionWithOptions(pack, "latest", ResolveOptions{}); err == nil {
		t.Fatalf("explicit staged dist-tag should fail without include-staged")
	}
	got, err = pickVersionWithOptions(pack, "latest", ResolveOptions{IncludeStaged: true})
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.1.0" {
		t.Fatalf("explicit staged dist-tag got %s want 1.1.0", got)
	}
	got, err = pickVersionWithOptions(pack, "^1.0.0", ResolveOptions{IncludeStaged: true})
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.0.0" {
		t.Fatalf("range should prefer non-staged version before semver, got %s", got)
	}
}

func TestPickVersionAvoidsPolicyRestrictedLatest(t *testing.T) {
	pack := &Packument{
		Name:     "demo",
		DistTags: map[string]string{"latest": "1.2.0"},
		Versions: map[string]VersionManifest{
			"1.1.0": {},
			"1.2.0": {},
		},
	}
	pack.PolicyRestrictions.Versions = map[string]VersionManifest{"1.2.0": {}}

	got, err := pickVersion(pack, "^1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.1.0" {
		t.Fatalf("got %s want non-restricted 1.1.0", got)
	}

	if _, err := pickVersion(pack, "latest"); err == nil {
		t.Fatalf("explicit restricted dist-tag should fail")
	}
}

func TestPickVersionErrorsWhenOnlyPolicyRestrictedVersionMatches(t *testing.T) {
	pack := &Packument{
		Name:     "demo",
		DistTags: map[string]string{"latest": "1.2.0"},
		Versions: map[string]VersionManifest{
			"1.0.0": {},
		},
	}
	pack.PolicyRestrictions.Versions = map[string]VersionManifest{"1.2.0": {Version: "1.2.0"}}

	if _, err := pickVersion(pack, "^1.2.0"); err == nil {
		t.Fatalf("restricted-only match should fail")
	}
}

func TestPickVersionAvoidRange(t *testing.T) {
	pack := &Packument{
		Name:     "demo",
		DistTags: map[string]string{"latest": "1.3.0"},
		Versions: map[string]VersionManifest{
			"1.1.0": {},
			"1.2.0": {},
			"1.3.0": {},
		},
	}

	got, err := pickVersionWithOptions(pack, "^1.0.0", ResolveOptions{Avoid: ">=1.3.0"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.2.0" {
		t.Fatalf("got %s want non-avoided 1.2.0", got)
	}
}

func TestPickVersionAvoidStrictFallsBackOutsideRequestedRange(t *testing.T) {
	pack := &Packument{
		Name:     "demo",
		DistTags: map[string]string{"latest": "1.1.0"},
		Versions: map[string]VersionManifest{
			"1.0.0": {},
			"1.1.0": {},
			"2.0.0": {},
		},
	}

	got, err := pickVersionWithOptions(pack, "^1.0.0", ResolveOptions{Avoid: "^1.0.0", AvoidStrict: true})
	if err != nil {
		t.Fatal(err)
	}
	if got != "2.0.0" {
		t.Fatalf("got %s want avoid-strict star fallback 2.0.0", got)
	}
}

func TestPickVersionWildcardAvoidsDeprecatedLatest(t *testing.T) {
	pack := &Packument{
		Name:     "demo",
		DistTags: map[string]string{"latest": "1.2.0"},
		Versions: map[string]VersionManifest{
			"1.1.0": {},
			"1.2.0": {Deprecated: "bad"},
		},
	}

	got, err := pickVersion(pack, "*")
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.1.0" {
		t.Fatalf("got %s want non-deprecated 1.1.0", got)
	}

	got, err = pickVersion(pack, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.2.0" {
		t.Fatalf("explicit latest got %s want deprecated dist-tag 1.2.0", got)
	}
}

func TestPickVersionPrefersEngineCompatibleManifest(t *testing.T) {
	pack := &Packument{
		Name:     "demo",
		DistTags: map[string]string{"latest": "1.2.0"},
		Versions: map[string]VersionManifest{
			"1.1.0": {Engines: map[string]string{"node": ">=1"}},
			"1.2.0": {Engines: map[string]string{"node": ">=99"}},
		},
	}

	got, err := pickVersionWithOptions(pack, "^1.0.0", ResolveOptions{NodeVersion: "20.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.1.0" {
		t.Fatalf("got %s want engine-compatible 1.1.0", got)
	}

	got, err = pickVersionWithOptions(pack, "^1.0.0", ResolveOptions{NodeVersion: "100.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.2.0" {
		t.Fatalf("got %s want latest-compatible 1.2.0", got)
	}
}

func TestPickVersionHonorsBeforeTime(t *testing.T) {
	pack := &Packument{
		Name:     "demo",
		DistTags: map[string]string{"latest": "2.0.0", "beta": "2.0.0"},
		Time: map[string]string{
			"1.0.0": "2024-01-01T00:00:00Z",
			"1.1.0": "2024-02-01T00:00:00Z",
			"2.0.0": "2024-03-01T00:00:00Z",
		},
		Versions: map[string]VersionManifest{
			"1.0.0": {},
			"1.1.0": {},
			"2.0.0": {},
		},
	}
	before := time.Date(2024, 2, 15, 0, 0, 0, 0, time.UTC)

	got, err := pickVersionWithOptions(pack, "^1.0.0", ResolveOptions{Before: before})
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.1.0" {
		t.Fatalf("range got %s want 1.1.0", got)
	}

	got, err = pickVersionWithOptions(pack, "latest", ResolveOptions{Before: before})
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.1.0" {
		t.Fatalf("tag fallback got %s want highest version before latest publish time", got)
	}

	if _, err := pickVersionWithOptions(pack, "2.0.0", ResolveOptions{Before: before}); err == nil {
		t.Fatalf("exact version published after before time should fail")
	}
}

func TestPrereleaseRangesRequireMatchingTupleComparator(t *testing.T) {
	tests := []struct {
		version string
		spec    string
		want    bool
	}{
		{"3.0.0-beta.1", ">=3.0.0-beta <4", true},
		{"3.0.1-beta.1", ">=3.0.0-beta <4", false},
		{"3.0.1-beta.1", ">=3.0.1-beta <4", true},
		{"3.1.0-beta.1", "^3.0.0-beta", false},
		{"3.0.1", "^3.0.0-beta", true},
		{"3.0.0-beta.2", "^3.0.0-beta", true},
		{"3.0.0-beta.1", ">=2.0.0-beta || >=3.0.0-beta", true},
		{"3.0.0-beta.1", ">=2.0.0-beta || >=3.1.0-beta", false},
	}
	for _, tc := range tests {
		if got := satisfies(tc.version, tc.spec); got != tc.want {
			t.Fatalf("satisfies(%q, %q) = %v want %v", tc.version, tc.spec, got, tc.want)
		}
	}
}

func TestStrictVersionParsingMatchesNpmSemver(t *testing.T) {
	tests := []struct {
		version string
		ok      bool
	}{
		{"1.2.3", true},
		{"v1.2.3", true},
		{"1.2.3-alpha.1", true},
		{"1.2.3+build.01", true},
		{"01.2.3", false},
		{"1.02.3", false},
		{"1.2.03", false},
		{"1.2.3-alpha.01", false},
		{"1.2.3-alpha..1", false},
		{"1.2.3+build..1", false},
	}
	for _, tc := range tests {
		if got := parseVersion(tc.version).ok; got != tc.ok {
			t.Fatalf("parseVersion(%q).ok = %v want %v", tc.version, got, tc.ok)
		}
	}
}

func TestPickVersionIgnoresInvalidPackumentVersions(t *testing.T) {
	pack := &Packument{
		Name:     "demo",
		DistTags: map[string]string{"latest": "1.2.3"},
		Versions: map[string]VersionManifest{
			"1.2.3":          {},
			"1.2.4-alpha.01": {},
			"01.9.9":         {},
		},
	}

	got, err := pickVersion(pack, ">=1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.2.3" {
		t.Fatalf("got %s want valid semver 1.2.3", got)
	}
	if satisfies("1.2.3-alpha.1", ">=1.2.3-alpha.01") {
		t.Fatalf("range with invalid prerelease comparator should not satisfy")
	}
}

func TestInvalidPartialRangesMatchNpmSemver(t *testing.T) {
	tests := []struct {
		version string
		spec    string
	}{
		{"1.2.3", "01"},
		{"1.2.3", "1.02"},
		{"1.2.3", "1.2.03"},
		{"1.2.3", "01.x"},
		{"1.2.3", "1.02.x"},
		{"1.2.3", "~01.2.3"},
		{"1.2.3", "^1.02.3"},
		{"2.0.0", ">01.2.3"},
		{"1.2.3", ">=1.2.3-alpha.01"},
	}
	for _, tc := range tests {
		if satisfies(tc.version, tc.spec) {
			t.Fatalf("satisfies(%q, %q) = true want false", tc.version, tc.spec)
		}
	}
}

func TestInvalidRangesMatchNpmSemver(t *testing.T) {
	tests := []struct {
		version string
		spec    string
	}{
		{"1.2.3", ">"},
		{"1.2.3", "<"},
		{"1.2.3", ">="},
		{"1.2.3", "<="},
		{"1.2.3", "="},
		{"1.2.3", ">bad"},
		{"1.2.3", "<bad"},
		{"1.2.3", ">=bad"},
		{"1.2.3", "=bad"},
		{"1.2.3", "bad"},
		{"1.2.3", "bad || 1.2.3"},
		{"1.2.3", "bad ||"},
		{"bad", "*"},
		{"01.2.3", "*"},
		{"1.2.3", "latest"},
	}
	for _, tc := range tests {
		if satisfies(tc.version, tc.spec) {
			t.Fatalf("satisfies(%q, %q) = true want false", tc.version, tc.spec)
		}
	}
}

func TestEmptyORDisjunctMatchesNpmSemverWildcard(t *testing.T) {
	tests := []string{
		"1.2.3 ||",
		"|| 1.2.3",
		"1.2.3 || || 2.0.0",
	}
	for _, spec := range tests {
		if !satisfies("1.2.3", spec) || !satisfies("2.0.0", spec) {
			t.Fatalf("empty OR disjunct %q should behave as wildcard for stable versions", spec)
		}
		if satisfies("2.0.0-beta.1", spec) {
			t.Fatalf("empty OR disjunct %q should not include prerelease without prerelease comparator", spec)
		}
	}
}

func TestRangeIntersectsMatchesNpmSemverCases(t *testing.T) {
	tests := []struct {
		a    string
		b    string
		want bool
	}{
		{"^1.2.0", "^1.0.0", true},
		{"1.2.0", "^1.0.0", true},
		{">1.2.0", "<1.2.2", true},
		{"1.2.0", "2.0.0", false},
		{"^1.0.0", "^2.0.0", false},
		{">=1 <2", ">=3", false},
		{"bad || 1.2.3", "1.2.3", false},
	}
	for _, tc := range tests {
		if got := rangeIntersects(tc.a, tc.b); got != tc.want {
			t.Fatalf("rangeIntersects(%q, %q) = %v want %v", tc.a, tc.b, got, tc.want)
		}
		if got := rangeIntersects(tc.b, tc.a); got != tc.want {
			t.Fatalf("rangeIntersects(%q, %q) = %v want %v", tc.b, tc.a, got, tc.want)
		}
	}
}

func TestCaretPartialRangesMatchNpmSemver(t *testing.T) {
	tests := []struct {
		version string
		spec    string
		want    bool
	}{
		{"0.0.0", "^0.0.x", true},
		{"0.0.1", "^0.0.x", true},
		{"0.1.0", "^0.0.x", false},
		{"0.0.3", "^0.0.3", true},
		{"0.0.4", "^0.0.3", false},
		{"0.1.0", "^0.1.x", true},
		{"0.1.1", "^0.1.x", true},
		{"0.2.0", "^0.1.x", false},
		{"0.1.1", "^0.x", true},
		{"1.0.0", "^0.x", false},
		{"1.9.0", "^1.2.x", true},
		{"2.0.0", "^1.2.x", false},
	}
	for _, tc := range tests {
		if got := satisfies(tc.version, tc.spec); got != tc.want {
			t.Fatalf("satisfies(%q, %q) = %v want %v", tc.version, tc.spec, got, tc.want)
		}
	}
}

func TestTildePartialRangesMatchNpmSemver(t *testing.T) {
	tests := []struct {
		version string
		spec    string
		want    bool
	}{
		{"0.0.0", "~0.0.x", true},
		{"0.0.1", "~0.0.x", true},
		{"0.1.0", "~0.0.x", false},
		{"0.1.0", "~0.x", true},
		{"1.0.0", "~0.x", false},
		{"1.2.0", "~1.2.x", true},
		{"1.2.9", "~1.2.x", true},
		{"1.3.0", "~1.2.x", false},
		{"1.3.0", "~1", true},
		{"2.0.0", "~1", false},
		{"1.2.3", "~>1.2.0", true},
		{"1.2.9", "~> 1.2.0", true},
		{"1.3.0", "~>1.2.0", false},
	}
	for _, tc := range tests {
		if got := satisfies(tc.version, tc.spec); got != tc.want {
			t.Fatalf("satisfies(%q, %q) = %v want %v", tc.version, tc.spec, got, tc.want)
		}
	}
}

func TestWildcardComparatorRangesMatchNpmSemver(t *testing.T) {
	tests := []struct {
		version string
		spec    string
		want    bool
	}{
		{"1.2.3", ">1.x", false},
		{"2.0.0", ">1.x", true},
		{"1.2.3", ">=1.x", true},
		{"0.9.9", "<1.x", true},
		{"1.0.0", "<1.x", false},
		{"1.9.9", "<=1.x", true},
		{"2.0.0", "<=1.x", false},
		{"1.2.3", ">1.2.x", false},
		{"1.3.0", ">1.2.x", true},
		{"1.1.9", "<1.2.x", true},
		{"1.2.0", "<1.2.x", false},
		{"1.2.9", "<=1.2.x", true},
		{"1.3.0", "<=1.2.x", false},
		{"1.2.3", "=1.2.x", true},
		{"1.3.0", "=1.2.x", false},
		{"1.2.3", ">x", false},
		{"1.2.3", "<x", false},
		{"1.2.3", ">=x", true},
		{"1.2.3", "<=x", true},
		{"1.2.3", "=x", true},
	}
	for _, tc := range tests {
		if got := satisfies(tc.version, tc.spec); got != tc.want {
			t.Fatalf("satisfies(%q, %q) = %v want %v", tc.version, tc.spec, got, tc.want)
		}
	}
}

func TestComparatorBuildMetadataIgnoredLikeNpmSemver(t *testing.T) {
	tests := []struct {
		version string
		spec    string
		want    bool
	}{
		{"1.2.3", "1.2.3+build", true},
		{"1.2.3+other", "1.2.3+build", true},
		{"1.2.4", "1.2.3+build", false},
		{"1.2.3", ">=1.2.3+build", true},
		{"1.2.4", ">=1.2.3+build", true},
		{"1.2.3", "<=1.2.3+build", true},
		{"1.2.4", "<=1.2.3+build", false},
		{"1.2.3", ">1.2.3+build", false},
		{"1.2.4", ">1.2.3+build", true},
	}
	for _, tc := range tests {
		if got := satisfies(tc.version, tc.spec); got != tc.want {
			t.Fatalf("satisfies(%q, %q) = %v want %v", tc.version, tc.spec, got, tc.want)
		}
	}
}

func TestGTEZeroComparatorMatchesNpmSemverWildcard(t *testing.T) {
	tests := []struct {
		version string
		spec    string
		want    bool
	}{
		{"1.2.3", ">=0.0.0", true},
		{"1.2.3-beta.1", ">=0.0.0", false},
		{"1.2.3", ">=0.0.0-0", true},
		{"1.2.3-beta.1", ">=0.0.0-0", false},
		{"0.0.0-beta.1", ">=0.0.0-0", true},
	}
	for _, tc := range tests {
		if got := satisfies(tc.version, tc.spec); got != tc.want {
			t.Fatalf("satisfies(%q, %q) = %v want %v", tc.version, tc.spec, got, tc.want)
		}
	}
}

func TestPartialComparatorRangesMatchNpmSemver(t *testing.T) {
	tests := []struct {
		version string
		spec    string
		want    bool
	}{
		{"1.2.0", ">1.2", false},
		{"1.2.9", ">1.2", false},
		{"1.3.0", ">1.2", true},
		{"1.2.0", ">=1.2", true},
		{"1.9.9", ">1", false},
		{"2.0.0", ">1", true},
		{"1.2.9", "<=1.2", true},
		{"1.3.0", "<=1.2", false},
		{"1.9.9", "<=1", true},
		{"2.0.0", "<=1", false},
		{"1.0.0", "<1", false},
		{"0.9.9", "<1", true},
		{"2.0.0", "<=1.x", false},
		{"1.9.9", "<=1.x", true},
		{"1.2.3", ">= 1.2.0", true},
		{"1.2.3", "> 1.2.0 < 2.0.0", true},
		{"1.2.3", "< 1.2.0 || >= 1.2.3", true},
	}
	for _, tc := range tests {
		if got := satisfies(tc.version, tc.spec); got != tc.want {
			t.Fatalf("satisfies(%q, %q) = %v want %v", tc.version, tc.spec, got, tc.want)
		}
	}
}

func TestHyphenRangesPreservePrereleaseLowerBound(t *testing.T) {
	tests := []struct {
		version string
		spec    string
		want    bool
	}{
		{"1.2.3-beta.1", "1.2.3-beta.1 - 1.2.3", true},
		{"1.2.3-alpha.1", "1.2.3-beta.1 - 1.2.3", false},
		{"1.2.3", "1.2.3-beta.1 - 1.2.3", true},
		{"1.2.4", "1.2.3-beta.1 - 1.2.3", false},
	}
	for _, tc := range tests {
		if got := satisfies(tc.version, tc.spec); got != tc.want {
			t.Fatalf("satisfies(%q, %q) = %v want %v", tc.version, tc.spec, got, tc.want)
		}
	}
}

func TestPickVersionDoesNotSelectUnmatchedPrereleaseTuple(t *testing.T) {
	pack := &Packument{
		Name:     "demo",
		DistTags: map[string]string{"latest": "2.0.0"},
		Versions: map[string]VersionManifest{
			"3.0.0-beta.1": {},
			"3.0.1-beta.1": {},
			"3.1.0-beta.1": {},
		},
	}

	got, err := pickVersion(pack, ">=3.0.0-beta <4")
	if err != nil {
		t.Fatal(err)
	}
	if got != "3.0.0-beta.1" {
		t.Fatalf("got %s want 3.0.0-beta.1", got)
	}
}

func TestParsePackageSpecAlias(t *testing.T) {
	name, spec, err := parsePackageSpec("alias", "npm:@scope/real@^2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if name != "@scope/real" || spec != "^2.0.0" {
		t.Fatalf("got %s %s", name, spec)
	}
}

func TestParsePackageSpecAliasWithoutSpecDefaultsToWildcard(t *testing.T) {
	name, spec, err := parsePackageSpec("alias", "npm:real")
	if err != nil {
		t.Fatal(err)
	}
	if name != "real" || spec != "*" {
		t.Fatalf("got %s %s, want real *", name, spec)
	}
}

func TestSatisfiesOrCaretRangeExcludesNextMajor(t *testing.T) {
	spec := "^7.0.0 || ^8.0.0 || ^9.0.0"
	if !satisfies("9.39.4", spec) {
		t.Fatalf("expected 9.39.4 to satisfy %q", spec)
	}
	if satisfies("10.3.0", spec) {
		t.Fatalf("expected 10.3.0 to NOT satisfy %q", spec)
	}
}
