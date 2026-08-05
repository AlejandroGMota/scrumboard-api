package testutil

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// update regenerates the golden files instead of comparing against them:
//
//	go test ./... -update
//
// Two rules keep golden files from becoming a burden (02-testing.md §6):
// reviewing the diff is part of reviewing the change, and non-deterministic
// fields are neutralised by the fake Clock and IDGen rather than by regular
// expressions. A golden file regenerated without looking is a deleted test.
var update = flag.Bool("update", false, "regenerate the golden files in testdata/golden")

// AssertGolden compares a JSON response against testdata/golden/<name>.json.
//
// It catches what no hand-written assertion catches well: that the shape of
// the JSON has not changed without anyone noticing. Renaming a Go field and
// forgetting the json tag breaks no value assertion, but it does break the
// frontend.
func AssertGolden(t *testing.T, name string, body []byte) {
	t.Helper()

	path := filepath.Join("testdata", "golden", name+".json")

	// Re-indent so the file is readable and the diff is line-by-line.
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, body, "", "  "); err != nil {
		t.Fatalf("the response is not valid JSON: %v\nbody: %s", err, body)
	}
	got := append(bytes.TrimRight(pretty.Bytes(), "\n"), '\n')

	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating the golden directory: %v", err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("writing the golden file: %v", err)
		}
		t.Logf("golden file updated: %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the golden file %s: %v\n\nRun `make golden-update` and review the result by hand.", path, err)
	}

	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("response does not match %s (-golden +got):\n%s\n\nIf the change is intended, run `make golden-update` and review the diff.", path, diff)
	}
}
