package avro

import (
	"encoding/json"
	"os"
	"testing"
)

func mustParse(t *testing.T, s string) *Schema {
	t.Helper()
	sch, err := Parse([]byte(s))
	if err != nil {
		t.Fatalf("parse %s: %v", s, err)
	}
	return sch
}

// TestApacheAvroCases runs the reader/writer pairs from Apache Avro's own
// compatibility test suite (see scripts/gen-avro-cases.py).
func TestApacheAvroCases(t *testing.T) {
	data, err := os.ReadFile("../../testdata/avro/apache-avro-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Cases []struct {
			Reader     json.RawMessage `json:"reader"`
			Writer     json.RawMessage `json:"writer"`
			Compatible bool            `json:"compatible"`
			Type       string          `json:"type"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.Cases) < 100 {
		t.Fatalf("expected the full corpus, got %d cases", len(corpus.Cases))
	}
	t.Logf("checking %d reader/writer cases from the Apache Avro test suite", len(corpus.Cases))
	for i, tc := range corpus.Cases {
		r := mustParse(t, string(tc.Reader))
		w := mustParse(t, string(tc.Writer))
		got := CheckReaderWriter(r, w)
		if tc.Compatible {
			if len(got) != 0 {
				t.Errorf("case %d: reader %s writer %s: want compatible, got %+v", i, tc.Reader, tc.Writer, got)
			}
			continue
		}
		if len(got) == 0 {
			t.Errorf("case %d: reader %s writer %s: want %s, got compatible", i, tc.Reader, tc.Writer, tc.Type)
			continue
		}
		if string(got[0].Type) != tc.Type {
			t.Errorf("case %d: reader %s writer %s: want %s, got %+v", i, tc.Reader, tc.Writer, tc.Type, got)
		}
	}
}

const userV1 = `{"namespace": "example.avro", "type": "record", "name": "user",
 "fields": [{"name": "name", "type": "string"}, {"name": "favorite_number", "type": "int"}]}`

func TestRules(t *testing.T) {
	tests := []struct {
		name           string
		reader, writer string
		wantType       IncompatibilityType // "" means compatible
		wantLocation   string
		wantField      string
	}{
		{
			// Confluent docs, "Backward compatibility": adding favorite_color
			// with a default is backward compatible.
			name:   "confluent docs: new field with default",
			reader: `{"namespace": "example.avro", "type": "record", "name": "user", "fields": [{"name": "name", "type": "string"}, {"name": "favorite_number", "type": "int"}, {"name": "favorite_color", "type": "string", "default": "green"}]}`,
			writer: userV1,
		},
		{
			// Same page: "Had the default value been omitted in the new
			// field, the new schema would not be backward compatible".
			name:         "confluent docs: new field without default",
			reader:       `{"namespace": "example.avro", "type": "record", "name": "user", "fields": [{"name": "name", "type": "string"}, {"name": "favorite_number", "type": "int"}, {"name": "favorite_color", "type": "string"}]}`,
			writer:       userV1,
			wantType:     ReaderFieldMissingDefaultValue,
			wantLocation: "/fields/2",
			wantField:    "user.favorite_color",
		},
		{
			// Same page, forward: dropping favorite_number (no default) is
			// not forward compatible. Old reader, new writer.
			name:         "confluent docs: removed field without default (forward)",
			reader:       userV1,
			writer:       `{"namespace": "example.avro", "type": "record", "name": "user", "fields": [{"name": "name", "type": "string"}]}`,
			wantType:     ReaderFieldMissingDefaultValue,
			wantLocation: "/fields/1",
			wantField:    "user.favorite_number",
		},
		{
			name:   "removed field is ignored by a new reader (backward)",
			reader: `{"type": "record", "name": "user", "fields": [{"name": "name", "type": "string"}]}`,
			writer: userV1,
		},
		{
			name:         "type narrowed long to int",
			reader:       `{"type": "record", "name": "Payment", "fields": [{"name": "amount_minor", "type": "int"}]}`,
			writer:       `{"type": "record", "name": "Payment", "fields": [{"name": "amount_minor", "type": "long"}]}`,
			wantType:     TypeMismatch,
			wantLocation: "/fields/0/type",
			wantField:    "Payment.amount_minor",
		},
		{
			name:   "type widened int to long",
			reader: `{"type": "record", "name": "Payment", "fields": [{"name": "amount_minor", "type": "long"}]}`,
			writer: `{"type": "record", "name": "Payment", "fields": [{"name": "amount_minor", "type": "int"}]}`,
		},
		{
			name:         "enum value removed",
			reader:       `{"type": "record", "name": "Order", "fields": [{"name": "status", "type": {"type": "enum", "name": "Status", "symbols": ["NEW", "PAID"]}}]}`,
			writer:       `{"type": "record", "name": "Order", "fields": [{"name": "status", "type": {"type": "enum", "name": "Status", "symbols": ["NEW", "PAID", "REFUNDED"]}}]}`,
			wantType:     MissingEnumSymbols,
			wantLocation: "/fields/0/type/symbols",
			wantField:    "Order.status",
		},
		{
			name:   "enum value removed but reader has enum default",
			reader: `{"type": "enum", "name": "Status", "symbols": ["NEW", "PAID", "UNKNOWN"], "default": "UNKNOWN"}`,
			writer: `{"type": "enum", "name": "Status", "symbols": ["NEW", "PAID", "REFUNDED"]}`,
		},
		{
			name:         "nullable made required",
			reader:       `{"type": "record", "name": "Order", "fields": [{"name": "coupon", "type": "string"}]}`,
			writer:       `{"type": "record", "name": "Order", "fields": [{"name": "coupon", "type": ["null", "string"], "default": null}]}`,
			wantType:     TypeMismatch,
			wantLocation: "/fields/0/type",
			wantField:    "Order.coupon<null>",
		},
		{
			name:         "union branch removed",
			reader:       `["null", "string"]`,
			writer:       `["null", "string", "long"]`,
			wantType:     MissingUnionBranch,
			wantLocation: "/2",
			wantField:    "$",
		},
		{
			name:         "record renamed without alias",
			reader:       `{"type": "record", "name": "OrderPlaced", "fields": []}`,
			writer:       `{"type": "record", "name": "OrderCreated", "fields": []}`,
			wantType:     NameMismatch,
			wantLocation: "/name",
			wantField:    "OrderPlaced",
		},
		{
			name:   "record renamed with alias",
			reader: `{"type": "record", "name": "OrderPlaced", "aliases": ["OrderCreated"], "fields": []}`,
			writer: `{"type": "record", "name": "OrderCreated", "fields": []}`,
		},
		{
			name:   "field renamed with alias",
			reader: `{"type": "record", "name": "R", "fields": [{"name": "customer_id", "aliases": ["customerId"], "type": "string"}]}`,
			writer: `{"type": "record", "name": "R", "fields": [{"name": "customerId", "type": "string"}]}`,
		},
		{
			name:         "fixed size changed",
			reader:       `{"type": "fixed", "name": "Hash", "size": 16}`,
			writer:       `{"type": "fixed", "name": "Hash", "size": 32}`,
			wantType:     FixedSizeMismatch,
			wantLocation: "/size",
			wantField:    "Hash",
		},
		{
			name:         "nested array item type narrowed",
			reader:       `{"type": "record", "name": "Cart", "fields": [{"name": "items", "type": {"type": "array", "items": {"type": "record", "name": "Line", "fields": [{"name": "qty", "type": "int"}]}}}]}`,
			writer:       `{"type": "record", "name": "Cart", "fields": [{"name": "items", "type": {"type": "array", "items": {"type": "record", "name": "Line", "fields": [{"name": "qty", "type": "double"}]}}}]}`,
			wantType:     TypeMismatch,
			wantLocation: "/fields/0/type/items/fields/0/type",
			wantField:    "Cart.items[].qty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CheckReaderWriter(mustParse(t, tt.reader), mustParse(t, tt.writer))
			if tt.wantType == "" {
				if len(got) != 0 {
					t.Fatalf("want compatible, got %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("want exactly one %s, got %+v", tt.wantType, got)
			}
			g := got[0]
			if g.Type != tt.wantType || g.Location != tt.wantLocation || g.Field != tt.wantField {
				t.Fatalf("got %s at %s (%s), want %s at %s (%s)", g.Type, g.Location, g.Field, tt.wantType, tt.wantLocation, tt.wantField)
			}
			if g.Message == "" {
				t.Fatal("empty message")
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	for _, s := range []string{
		`{`,
		`{"type": "record"}`,
		`{"type": "record", "name": "R", "fields": [{"type": "int"}]}`,
		`{"type": "record", "name": "R", "fields": [{"name": "a", "type": "Nope"}]}`,
		`{"type": "record", "name": "R", "fields": [{"name": "a", "type": "int"}, {"name": "a", "type": "int"}]}`,
		`["int", "int"]`,
		`["int", ["string"]]`,
		`{"type": "fixed", "name": "F"}`,
		`[{"type": "record", "name": "R", "fields": []}, {"type": "record", "name": "R", "fields": []}]`,
	} {
		if _, err := Parse([]byte(s)); err == nil {
			t.Errorf("Parse(%s): want error", s)
		}
	}
}

func TestParseNamespacesAndReferences(t *testing.T) {
	s := mustParse(t, `{"type": "record", "name": "Order", "namespace": "shop",
	  "fields": [
	    {"name": "total", "type": {"type": "record", "name": "Money", "fields": [{"name": "minor", "type": "long"}]}},
	    {"name": "refund", "type": ["null", "Money"], "default": null},
	    {"name": "tax", "type": {"type": "shop.Money"}},
	    {"name": "placed_at", "type": {"type": "long", "logicalType": "timestamp-millis"}}
	  ]}`)
	if s.FullName() != "shop.Order" {
		t.Fatalf("full name = %s", s.FullName())
	}
	if s.Fields[0].Type != s.Fields[1].Type.Branches[1] || s.Fields[2].Type != s.Fields[0].Type {
		t.Fatal("named type references should share the definition")
	}
	if s.Fields[3].Type.LogicalType != "timestamp-millis" {
		t.Fatal("logical type lost")
	}
	if got := s.Fields[1].Type.Label(); got != "union [null, record shop.Money]" {
		t.Fatalf("label = %q", got)
	}
}
