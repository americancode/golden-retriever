package npm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSyncTargetMarksPresentAndMissingPackages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/present":
			fmt.Fprintf(w, `{
  "name": "present",
  "dist-tags": {"latest": "1.0.0"},
  "versions": {
    "1.0.0": {
      "name": "present",
      "version": "1.0.0",
      "dist": {
        "tarball": "%s/present/-/present-1.0.0.tgz",
        "integrity": "sha512-present"
      }
    }
  }
}`, serverURL(r))
		case "/missing-version":
			fmt.Fprint(w, `{
  "name": "missing-version",
  "dist-tags": {"latest": "2.0.0"},
  "versions": {
    "2.0.0": {"name": "missing-version", "version": "2.0.0"}
  }
}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	state := NewState()
	report, err := SyncTarget(context.Background(), NewClient(srv.URL), state, []Package{
		{Name: "present", Version: "1.0.0", Tarball: "https://source/present.tgz", Integrity: "sha512-source"},
		{Name: "missing-version", Version: "1.0.0"},
		{Name: "missing-package", Version: "1.0.0"},
	}, SyncTargetOptions{Concurrency: 3, Source: "test-target"})
	if err != nil {
		t.Fatal(err)
	}
	if report.Present != 1 || report.Missing != 2 || report.Failed != 0 {
		t.Fatalf("unexpected report: %#v", report)
	}
	rec := state.Target["present@1.0.0"]
	if rec.Name != "present" || rec.Version != "1.0.0" || rec.Source != "test-target" {
		t.Fatalf("unexpected target record: %#v", rec)
	}
	if rec.Tarball == "https://source/present.tgz" {
		t.Fatalf("target record should use target registry tarball when available: %#v", rec)
	}
}
