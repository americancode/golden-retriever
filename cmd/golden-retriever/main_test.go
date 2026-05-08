package main

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"golden-retriever/internal/npm"
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

func TestPrintEngineWarnings(t *testing.T) {
	graph := npm.NewGraph()
	graph.AddEngineWarning(&npm.PackageEngineError{
		Package: "engine-package@1.0.0",
		Engine:  "node",
		Wanted:  ">=20",
		Current: "12.18.4",
	})

	oldStderr := os.Stderr
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = write
	defer func() { os.Stderr = oldStderr }()
	printEngineWarnings(graph)
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if !strings.Contains(got, "warn EBADENGINE package=engine-package@1.0.0 required=node@>=20 current=12.18.4") {
		t.Fatalf("unexpected warning output: %q", got)
	}
}
