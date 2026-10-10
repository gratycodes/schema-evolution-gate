package compat

import (
	"strings"
	"testing"

	"github.com/gratycodes/schema-evolution-gate/internal/jsonschema"
)

// Three Avro versions that are compatible step by step but not transitively:
// v2 adds email with a default, v3 drops the default. v3 can read v2 data
// (email is present) but not v1 data (email missing, no default).
var (
	v1 = Version{"v1", []byte(`{"type": "record", "name": "Customer", "fields": [{"name": "id", "type": "string"}]}`)}
	v2 = Version{"v2", []byte(`{"type": "record", "name": "Customer", "fields": [{"name": "id", "type": "string"}, {"name": "email", "type": "string", "default": ""}]}`)}
	v3 = Version{"v3", []byte(`{"type": "record", "name": "Customer", "fields": [{"name": "id", "type": "string"}, {"name": "email", "type": "string"}]}`)}
)

func TestModes(t *testing.T) {
	tests := []struct {
		mode        Mode
		history     []Version
		candidate   Version
		compatible  bool
		wantDirs    []Direction
		wantAgainst []string
	}{
		{Backward, []Version{v1, v2}, v3, true, nil, nil},
		{BackwardTransitive, []Version{v1, v2}, v3, false, []Direction{DirBackward}, []string{"v1"}},
		{Forward, []Version{v1}, v2, true, nil, nil},
		// v3 written, v1 reader: email ignored, fine. v2 reader: fine.
		{ForwardTransitive, []Version{v1, v2}, v3, true, nil, nil},
		// v1 written, v3 reader fails backward; forward is fine.
		{Full, []Version{v1}, v3, false, []Direction{DirBackward}, []string{"v1"}},
		{FullTransitive, []Version{v1, v2}, v3, false, []Direction{DirBackward}, []string{"v1"}},
		// Removing email (no default in v3) breaks forward: v3 readers need it.
		{Forward, []Version{v3}, v1, false, []Direction{DirForward}, []string{"v3"}},
		{None, []Version{v3}, v1, true, nil, nil},
	}
	for _, tt := range tests {
		t.Run(string(tt.mode)+"_"+tt.candidate.Label, func(t *testing.T) {
			res, err := Check(Options{Format: Avro, Mode: tt.mode}, tt.history, tt.candidate)
			if err != nil {
				t.Fatal(err)
			}
			if res.Compatible != tt.compatible {
				t.Fatalf("compatible = %v, want %v: %+v", res.Compatible, tt.compatible, res.Issues)
			}
			if len(res.Issues) != len(tt.wantDirs) {
				t.Fatalf("issues = %+v, want %d", res.Issues, len(tt.wantDirs))
			}
			for i, is := range res.Issues {
				if is.Direction != tt.wantDirs[i] || is.Against != tt.wantAgainst[i] {
					t.Errorf("issue %d = %s against %s, want %s against %s", i, is.Direction, is.Against, tt.wantDirs[i], tt.wantAgainst[i])
				}
				if is.Rule != "READER_FIELD_MISSING_DEFAULT_VALUE" || is.Field != "Customer.email" {
					t.Errorf("issue %d = %+v", i, is)
				}
			}
		})
	}
}

func TestTransitiveComparesNewestFirst(t *testing.T) {
	res, err := Check(Options{Format: Avro, Mode: BackwardTransitive}, []Version{v1, v2}, v3)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Compared, ",") != "v2,v1" {
		t.Fatalf("compared %v", res.Compared)
	}
}

func TestJSONSchemaReasonsAndPolicy(t *testing.T) {
	old := Version{"base", []byte(`{"type": "object", "properties": {"id": {"type": "string"}}}`)}
	cand := Version{"head", []byte(`{"type": "object", "properties": {"id": {"type": "string"}, "note": {"type": "string"}}}`)}

	strict, err := Check(Options{Format: JSONSchema, Mode: Backward}, []Version{old}, cand)
	if err != nil {
		t.Fatal(err)
	}
	if strict.Compatible || strict.Issues[0].Field != "note" || !strings.Contains(strict.Issues[0].Reason, "open content model") {
		t.Fatalf("strict: %+v", strict)
	}
	lenient, err := Check(Options{Format: JSONSchema, Mode: Backward, JSONPolicy: jsonschema.Lenient}, []Version{old}, cand)
	if err != nil {
		t.Fatal(err)
	}
	if !lenient.Compatible || lenient.Changes != 1 {
		t.Fatalf("lenient: %+v", lenient)
	}
}

func TestForwardReasonWording(t *testing.T) {
	oldEnum := Version{"base", []byte(`{"type": "enum", "name": "Status", "symbols": ["NEW", "PAID"]}`)}
	newEnum := Version{"head", []byte(`{"type": "enum", "name": "Status", "symbols": ["NEW", "PAID", "REFUNDED"]}`)}
	res, err := Check(Options{Format: Avro, Mode: Full}, []Version{oldEnum}, newEnum)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Issues) != 1 || res.Issues[0].Direction != DirForward || !strings.Contains(res.Issues[0].Reason, "enum symbol added") {
		t.Fatalf("%+v", res.Issues)
	}
}

func TestParseErrorsAreLabelled(t *testing.T) {
	_, err := Check(Options{Format: Avro, Mode: Backward}, []Version{v1}, Version{"head:orders.avsc", []byte(`{"type": "record"}`)})
	if err == nil || !strings.Contains(err.Error(), "head:orders.avsc") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseModeAndFormat(t *testing.T) {
	if m, _ := ParseMode("full_transitive"); m != FullTransitive {
		t.Fatal(m)
	}
	if m, _ := ParseMode(""); m != Backward {
		t.Fatal("default mode should be BACKWARD like Confluent")
	}
	if _, err := ParseMode("SIDEWAYS"); err == nil {
		t.Fatal("want error")
	}
	if _, err := ParseFormat("protobuf"); err == nil || !strings.Contains(err.Error(), "buf breaking") {
		t.Fatalf("err = %v", err)
	}
	for path, want := range map[string]Format{"a/b.avsc": Avro, "x.json": JSONSchema, "x.avsc.json": Avro} {
		if got, err := FormatFromPath(path); err != nil || got != want {
			t.Errorf("FormatFromPath(%s) = %v, %v", path, got, err)
		}
	}
	if _, err := FormatFromPath("x.proto"); err == nil {
		t.Fatal("want error")
	}
}

func TestFieldFromPointer(t *testing.T) {
	for in, want := range map[string]string{
		"#/":                  "(root)",
		"#/properties/amount": "amount",
		"#/required/amount":   "amount",
		"#/properties/lines/items/properties/qty": "lines[].qty",
		"#/properties/a~1b/maxLength":             "a/b",
		"#/items/2":                               "[]",
	} {
		if got := FieldFromPointer(in); got != want {
			t.Errorf("FieldFromPointer(%q) = %q, want %q", in, got, want)
		}
	}
}
