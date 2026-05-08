package main

import "testing"

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
