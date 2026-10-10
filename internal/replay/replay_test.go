package replay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gratycodes/schema-evolution-gate/internal/avro"
	"github.com/gratycodes/schema-evolution-gate/internal/compat"
)

func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadSamples(t *testing.T) {
	dir := writeFiles(t, map[string]string{
		"b.json":   `{"id": "b"}`,
		"a.jsonl":  "{\"id\": \"a1\"}\n\n{\"id\": \"a2\"}\n",
		"note.txt": "ignored",
	})
	got, err := LoadSamples(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range got {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "a.jsonl:1,a.jsonl:3,b.json" {
		t.Fatalf("names = %v", names)
	}
	if _, err := LoadSamples(writeFiles(t, map[string]string{"x.jsonl": "{nope"})); err == nil || !strings.Contains(err.Error(), "x.jsonl:1") {
		t.Fatalf("err = %v", err)
	}
	if _, err := LoadSamples(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("want error")
	}
}

// The JSON Schema change below looks safe to a rules engine that only sees
// "maxLength: 64 -> 16" as a tightening; replay shows how many real events
// it would reject, and which.
func TestJSONReplay(t *testing.T) {
	base := []byte(`{"type": "object", "properties": {"merchant_ref": {"type": "string", "maxLength": 64}}, "required": ["merchant_ref"]}`)
	head := []byte(`{"type": "object", "properties": {"merchant_ref": {"type": "string", "maxLength": 16}}, "required": ["merchant_ref"]}`)
	dir := writeFiles(t, map[string]string{
		"events.jsonl": `{"merchant_ref": "short"}
{"merchant_ref": "a-reference-that-is-too-long"}
{"merchant_ref": 42}
`,
	})
	samples, err := LoadSamples(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Run(compat.JSONSchema, base, head, samples, avro.AvroJSON)
	if err != nil {
		t.Fatal(err)
	}
	if res.Total != 3 || res.Failed != 1 || res.Stale != 1 {
		t.Fatalf("res = %+v", res)
	}
	f := res.Failures[0]
	if f.Sample != "events.jsonl:2" || f.Path != "/merchant_ref" || !strings.Contains(f.Message, "16") {
		t.Fatalf("failure = %+v", f)
	}
	if res.StaleExamples[0].Sample != "events.jsonl:3" {
		t.Fatalf("stale = %+v", res.StaleExamples)
	}
}

func TestAvroReplayUsesBaseAsWriter(t *testing.T) {
	base := []byte(`{"type": "record", "name": "Payment", "fields": [{"name": "state", "type": {"type": "enum", "name": "State", "symbols": ["AUTHORIZED", "CAPTURED", "VOIDED"]}}]}`)
	head := []byte(`{"type": "record", "name": "Payment", "fields": [{"name": "state", "type": {"type": "enum", "name": "State", "symbols": ["AUTHORIZED", "CAPTURED"]}}]}`)
	samples := []Sample{
		{Name: "p1", Data: map[string]any{"state": "CAPTURED"}},
		{Name: "p2", Data: map[string]any{"state": "VOIDED"}},
	}
	res, err := Run(compat.Avro, base, head, samples, avro.AvroJSON)
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 1 || res.Failures[0].Sample != "p2" || res.Failures[0].Path != "/state" {
		t.Fatalf("res = %+v", res)
	}
	// Without a base (new subject) samples are simply validated.
	res, err = Run(compat.Avro, nil, head, samples, avro.AvroJSON)
	if err != nil || res.Failed != 1 {
		t.Fatalf("res = %+v, err = %v", res, err)
	}
}

func TestRunErrors(t *testing.T) {
	if _, err := Run(compat.Avro, nil, []byte(`{`), nil, avro.AvroJSON); err == nil {
		t.Fatal("want error")
	}
	if _, err := Run(compat.JSONSchema, nil, []byte(`{"type": 5}`), nil, avro.AvroJSON); err == nil {
		t.Fatal("want error")
	}
	if _, err := Run("protobuf", nil, nil, nil, avro.AvroJSON); err == nil {
		t.Fatal("want error")
	}
}
