package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/gratycodes/schema-evolution-gate/internal/avro"
	"github.com/gratycodes/schema-evolution-gate/internal/compat"
	"github.com/gratycodes/schema-evolution-gate/internal/jsonschema"
)

func TestParse(t *testing.T) {
	c, err := Parse([]byte(`
version: 1
defaults:
  mode: full
  sampleEncoding: json
topics:
  - topic: orders
    schema: schemas/orders.avsc
    history: schemas/history/orders.v*.avsc
    samples: samples/orders
  - topic: refunds
    subject: refunds-events
    schema: schemas/refund.schema.json
    mode: BACKWARD_TRANSITIVE
    jsonPolicy: lenient
`), "/repo")
	if err != nil {
		t.Fatal(err)
	}
	o, r := c.Topics[0], c.Topics[1]
	if o.Subject != "orders-value" || o.ResolvedFormat != compat.Avro || o.ResolvedMode != compat.Full ||
		o.ResolvedEncoding != avro.PlainJSON || o.ResolvedPolicy != jsonschema.Strict {
		t.Fatalf("orders = %+v", o)
	}
	if r.Subject != "refunds-events" || r.ResolvedFormat != compat.JSONSchema || r.ResolvedMode != compat.BackwardTransitive ||
		r.ResolvedPolicy != jsonschema.Lenient {
		t.Fatalf("refunds = %+v", r)
	}
	if c.Path("schemas/orders.avsc") != filepath.Join("/repo", "schemas/orders.avsc") || c.Path("/abs") != "/abs" {
		t.Fatal("path resolution")
	}
}

func TestParseErrors(t *testing.T) {
	for _, tc := range []struct{ yaml, want string }{
		{`topics: []`, "no topics"},
		{`version: 2
topics: [{topic: a, schema: a.avsc}]`, "unsupported version"},
		{`topics: [{schema: a.avsc}]`, "topic is required"},
		{`topics: [{topic: a}]`, "schema is required"},
		{`topics: [{topic: a, schema: a.proto}]`, "cannot infer"},
		{`topics: [{topic: a, schema: a.avsc, mode: SIDEWAYS}]`, "unknown compatibility mode"},
		{`topics: [{topic: a, schema: a.json, jsonPolicy: loose}]`, "jsonPolicy"},
		{`topics: [{topic: a, schema: a.avsc, sampleEncoding: xml}]`, "sampleEncoding"},
		{`topics: [{topic: a, schema: a.avsc}, {topic: a, schema: b.avsc}]`, "duplicate subject"},
		{`topics: [{topic: a, schema: a.avsc, typo: 1}]`, "typo"},
	} {
		_, err := Parse([]byte(tc.yaml), "/repo")
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%q: err = %v, want %q", tc.yaml, err, tc.want)
		}
	}
}

func TestHistoryFilesNaturalOrder(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"orders.v10.avsc", "orders.v2.avsc", "orders.v1.avsc", "other.avsc"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	c := &Config{Dir: dir}
	files, err := c.HistoryFiles(Topic{History: "orders.v*.avsc"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	if !slices.Equal(names, []string{"orders.v1.avsc", "orders.v2.avsc", "orders.v10.avsc"}) {
		t.Fatalf("got %v", names)
	}
	if files, _ := c.HistoryFiles(Topic{}); files != nil {
		t.Fatal("no history configured")
	}
}

func TestNaturalLess(t *testing.T) {
	if !NaturalLess("v2", "v10") || NaturalLess("v10", "v2") || !NaturalLess("a", "b") || !NaturalLess("v1", "v1a") {
		t.Fatal("natural order")
	}
}
