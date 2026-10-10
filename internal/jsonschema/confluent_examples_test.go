package jsonschema

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestConfluentDiffExamples replays Confluent Schema Registry's own JSON
// Schema diff examples (json-schema-provider/src/test/resources, pinned by
// scripts/fetch-confluent-examples.sh) and requires the same compatible /
// incompatible verdict and the same list of classified changes (type and
// path) for every case. The files are not vendored because
// they are under the Confluent Community License; CI downloads them.
func TestConfluentDiffExamples(t *testing.T) {
	dir := os.Getenv("CONFLUENT_EXAMPLES_DIR")
	if dir == "" {
		dir = filepath.Join("..", "..", "testdata", "confluent")
	}
	files := []string{"diff-schema-examples.json", "diff-combined-schema-examples.json"}
	if _, err := os.Stat(filepath.Join(dir, files[0])); err != nil {
		if os.Getenv("REQUIRE_CONFLUENT_EXAMPLES") != "" {
			t.Fatalf("confluent examples missing in %s: run scripts/fetch-confluent-examples.sh", dir)
		}
		t.Skip("confluent examples not downloaded; run scripts/fetch-confluent-examples.sh")
	}
	total, agree := 0, 0
	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			t.Fatal(err)
		}
		var cases []struct {
			Description string          `json:"description"`
			Original    json.RawMessage `json:"original_schema"`
			Update      json.RawMessage `json:"update_schema"`
			Changes     []string        `json:"changes"`
			Compatible  bool            `json:"compatible"`
		}
		if err := json.Unmarshal(data, &cases); err != nil {
			t.Fatal(err)
		}
		for i, tc := range cases {
			total++
			o, err := Parse(tc.Original)
			if err != nil {
				t.Errorf("%s #%d %q: original: %v", f, i, tc.Description, err)
				continue
			}
			u, err := Parse(tc.Update)
			if err != nil {
				t.Errorf("%s #%d %q: update: %v", f, i, tc.Description, err)
				continue
			}
			diffs := Compare(o, u, Strict)
			got := len(Incompatible(diffs, Strict)) == 0
			if got != tc.Compatible {
				t.Errorf("%s #%d %q: compatible=%v, Confluent says %v\n  ours:     %v\n  confluent: %v",
					f, i, tc.Description, got, tc.Compatible, diffs, tc.Changes)
				continue
			}
			var gotChanges []string
			for _, d := range diffs {
				gotChanges = append(gotChanges, d.Type+" "+d.Path)
			}
			slices.Sort(gotChanges)
			want := slices.Clone(tc.Changes)
			slices.Sort(want)
			if !slices.Equal(gotChanges, want) {
				t.Errorf("%s #%d %q: same verdict, different changes\n  ours:      %v\n  confluent: %v",
					f, i, tc.Description, gotChanges, want)
				continue
			}
			agree++
		}
	}
	t.Logf("agreed with Confluent on %d/%d examples", agree, total)
}
