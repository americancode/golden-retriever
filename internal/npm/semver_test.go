package npm

import "testing"

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
