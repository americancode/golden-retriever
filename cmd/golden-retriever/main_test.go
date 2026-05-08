package main

import (
	"testing"
	"time"
)

func TestDependencySelectionOmitIncludePrecedence(t *testing.T) {
	set, err := dependencySelection(true, true, "dev,optional,peer", "optional,peer")
	if err != nil {
		t.Fatal(err)
	}
	if set.includeDev {
		t.Fatalf("dev should be omitted")
	}
	if !set.includeOptional {
		t.Fatalf("include should restore optional after omit")
	}
	if set.omitPeer {
		t.Fatalf("include should restore peer after omit")
	}
}

func TestDependencySelectionRejectsUnknownTypes(t *testing.T) {
	if _, err := dependencySelection(true, true, "prod", ""); err == nil {
		t.Fatalf("unknown omit type should fail")
	}
	if _, err := dependencySelection(true, true, "", "bundle"); err == nil {
		t.Fatalf("unknown include type should fail")
	}
}

func TestParseBefore(t *testing.T) {
	before, err := parseBefore("2024-02-15T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if !before.Equal(time.Date(2024, 2, 15, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("before = %s", before)
	}
	if empty, err := parseBefore(""); err != nil || !empty.IsZero() {
		t.Fatalf("empty before = %s err=%v", empty, err)
	}
	if _, err := parseBefore("2024-02-15"); err == nil {
		t.Fatalf("invalid before should fail")
	}
}
