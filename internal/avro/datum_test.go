package avro

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func datum(t *testing.T, s string) any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

const orderV1 = `{"type": "record", "name": "Order", "fields": [
  {"name": "id", "type": "string"},
  {"name": "amount_minor", "type": "long"},
  {"name": "status", "type": {"type": "enum", "name": "Status", "symbols": ["NEW", "PAID", "REFUNDED"]}},
  {"name": "coupon", "type": ["null", "string"], "default": null},
  {"name": "lines", "type": {"type": "array", "items": {"type": "record", "name": "Line", "fields": [{"name": "sku", "type": "string"}, {"name": "qty", "type": "int"}]}}}
]}`

func TestValidate(t *testing.T) {
	s := mustParse(t, orderV1)
	tests := []struct {
		name    string
		enc     Encoding
		payload string
		wantErr string // substring of the first error path+message, "" means valid
	}{
		{"avro json encoding", AvroJSON, `{"id": "o-1", "amount_minor": 1250, "status": "PAID", "coupon": {"string": "WELCOME"}, "lines": [{"sku": "A", "qty": 1}]}`, ""},
		{"null union", AvroJSON, `{"id": "o-1", "amount_minor": 1, "status": "NEW", "coupon": null, "lines": []}`, ""},
		{"unwrapped union rejected in avro json", AvroJSON, `{"id": "o-1", "amount_minor": 1, "status": "NEW", "coupon": "WELCOME", "lines": []}`, "/coupon: value"},
		{"unwrapped union accepted in plain json", PlainJSON, `{"id": "o-1", "amount_minor": 1, "status": "NEW", "coupon": "WELCOME", "lines": []}`, ""},
		{"defaulted field omitted in plain json", PlainJSON, `{"id": "o-1", "amount_minor": 1, "status": "NEW", "lines": []}`, ""},
		{"missing required field", AvroJSON, `{"id": "o-1", "status": "NEW", "coupon": null, "lines": []}`, "/amount_minor: required field"},
		{"bad enum symbol", AvroJSON, `{"id": "o-1", "amount_minor": 1, "status": "LOST", "coupon": null, "lines": []}`, "/status: expected one of"},
		{"int overflow", AvroJSON, `{"id": "o-1", "amount_minor": 1, "status": "NEW", "coupon": null, "lines": [{"sku": "A", "qty": 3000000000}]}`, "/lines/0/qty: expected int"},
		{"wrong type", AvroJSON, `{"id": 7, "amount_minor": 1, "status": "NEW", "coupon": null, "lines": []}`, "/id: expected string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := Validate(s, datum(t, tt.payload), tt.enc)
			if tt.wantErr == "" {
				if len(errs) != 0 {
					t.Fatalf("want valid, got %v", errs)
				}
				return
			}
			if len(errs) == 0 || !strings.Contains(errs[0].Error(), tt.wantErr) {
				t.Fatalf("want %q, got %v", tt.wantErr, errs)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	writer := mustParse(t, orderV1)
	// v2 drops REFUNDED, adds a currency without default, widens qty to long.
	reader := mustParse(t, `{"type": "record", "name": "Order", "fields": [
	  {"name": "id", "type": "string"},
	  {"name": "amount_minor", "type": "long"},
	  {"name": "status", "type": {"type": "enum", "name": "Status", "symbols": ["NEW", "PAID"]}},
	  {"name": "coupon", "type": ["null", "string"], "default": null},
	  {"name": "lines", "type": {"type": "array", "items": {"type": "record", "name": "Line", "fields": [{"name": "sku", "type": "string"}, {"name": "qty", "type": "long"}]}}}
	]}`)
	paid := datum(t, `{"id": "o-1", "amount_minor": 1, "status": "PAID", "coupon": null, "lines": [{"sku": "A", "qty": 2}]}`)
	if errs := Resolve(reader, writer, paid, AvroJSON); len(errs) != 0 {
		t.Fatalf("PAID order should resolve, got %v", errs)
	}
	refunded := datum(t, `{"id": "o-2", "amount_minor": 1, "status": "REFUNDED", "coupon": {"string": "X"}, "lines": []}`)
	errs := Resolve(reader, writer, refunded, AvroJSON)
	if len(errs) != 1 || errs[0].Path != "/status" || !strings.Contains(errs[0].Message, `"REFUNDED"`) {
		t.Fatalf("got %v", errs)
	}

	withCurrency := mustParse(t, `{"type": "record", "name": "Order", "fields": [{"name": "id", "type": "string"}, {"name": "currency", "type": "string"}]}`)
	errs = Resolve(withCurrency, writer, paid, AvroJSON)
	if len(errs) != 1 || errs[0].Path != "/currency" {
		t.Fatalf("got %v", errs)
	}

	// Reader union picks the first branch that can read the writer type.
	nullableLong := mustParse(t, `["null", "long"]`)
	if errs := Resolve(nullableLong, mustParse(t, `"int"`), datum(t, `5`), AvroJSON); len(errs) != 0 {
		t.Fatalf("got %v", errs)
	}
	if errs := Resolve(mustParse(t, `"int"`), mustParse(t, `"string"`), datum(t, `"x"`), AvroJSON); len(errs) != 1 {
		t.Fatalf("got %v", errs)
	}
}
