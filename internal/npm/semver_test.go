package npm

import (
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
