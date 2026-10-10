package jsonschema

import (
	"slices"
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

// Each case is an old (original) and a new (update) schema checked in the
// BACKWARD direction: can the new schema validate data the old one allowed?
func TestRules(t *testing.T) {
	const order = `{"type": "object", "properties": {"id": {"type": "string"}, "amount": {"type": "number"}}, "required": ["id"], "additionalProperties": false}`
	tests := []struct {
		name     string
		old, new string
		policy   Policy
		breaking []string // "TYPE path", empty means compatible
	}{
		{
			name:     "removed required field from closed model",
			old:      order,
			new:      `{"type": "object", "properties": {"amount": {"type": "number"}}, "additionalProperties": false}`,
			breaking: []string{"PROPERTY_REMOVED_FROM_CLOSED_CONTENT_MODEL #/properties/id"},
		},
		{
			name: "removed field allowed under lenient policy",
			old:  order, policy: Lenient,
			new: `{"type": "object", "properties": {"amount": {"type": "number"}}, "additionalProperties": false}`,
		},
		{
			name: "optional field added to closed model",
			old:  order,
			new:  `{"type": "object", "properties": {"id": {"type": "string"}, "amount": {"type": "number"}, "note": {"type": "string"}}, "required": ["id"], "additionalProperties": false}`,
		},
		{
			name:     "required field added without default",
			old:      order,
			new:      `{"type": "object", "properties": {"id": {"type": "string"}, "amount": {"type": "number"}, "currency": {"type": "string"}}, "required": ["id", "currency"], "additionalProperties": false}`,
			breaking: []string{"REQUIRED_PROPERTY_ADDED_TO_UNOPEN_CONTENT_MODEL #/properties/currency"},
		},
		{
			name: "required field added with default",
			old:  order,
			new:  `{"type": "object", "properties": {"id": {"type": "string"}, "amount": {"type": "number"}, "currency": {"type": "string", "default": "EUR"}}, "required": ["id", "currency"], "additionalProperties": false}`,
		},
		{
			name:     "existing field made required",
			old:      order,
			new:      `{"type": "object", "properties": {"id": {"type": "string"}, "amount": {"type": "number"}}, "required": ["id", "amount"], "additionalProperties": false}`,
			breaking: []string{"REQUIRED_ATTRIBUTE_ADDED #/required/amount"},
		},
		{
			name:     "type narrowed number to integer",
			old:      order,
			new:      `{"type": "object", "properties": {"id": {"type": "string"}, "amount": {"type": "integer"}}, "required": ["id"], "additionalProperties": false}`,
			breaking: []string{"TYPE_NARROWED #/properties/amount"},
		},
		{
			name: "type widened integer to number",
			old:  `{"type": "integer"}`,
			new:  `{"type": "number"}`,
		},
		{
			name:     "type changed",
			old:      `{"type": "object", "properties": {"id": {"type": "string"}}}`,
			new:      `{"type": "object", "properties": {"id": {"type": "integer"}}}`,
			breaking: []string{"TYPE_CHANGED #/properties/id"},
		},
		{
			name:     "enum value removed",
			old:      `{"type": "string", "enum": ["NEW", "PAID", "REFUNDED"]}`,
			new:      `{"type": "string", "enum": ["NEW", "PAID"]}`,
			breaking: []string{"COMBINED_TYPE_SUBSCHEMAS_CHANGED #/"},
		},
		{
			name:     "bare enum value removed",
			old:      `{"enum": ["NEW", "PAID", "REFUNDED"]}`,
			new:      `{"enum": ["NEW", "PAID"]}`,
			breaking: []string{"ENUM_ARRAY_NARROWED #/enum"},
		},
		{
			name: "enum value added",
			old:  `{"enum": ["NEW", "PAID"]}`,
			new:  `{"enum": ["NEW", "PAID", "REFUNDED"]}`,
		},
		{
			name:     "maxLength tightened",
			old:      `{"type": "string", "maxLength": 64}`,
			new:      `{"type": "string", "maxLength": 32}`,
			breaking: []string{"MAX_LENGTH_DECREASED #/maxLength"},
		},
		{
			name:     "pattern added",
			old:      `{"type": "string"}`,
			new:      `{"type": "string", "pattern": "^[A-Z]{3}$"}`,
			breaking: []string{"PATTERN_ADDED #/pattern"},
		},
		{
			name:     "minimum raised",
			old:      `{"type": "number", "minimum": 0}`,
			new:      `{"type": "number", "minimum": 1}`,
			breaking: []string{"MINIMUM_INCREASED #/minimum"},
		},
		{
			name:     "content model closed",
			old:      `{"type": "object", "properties": {"id": {"type": "string"}}}`,
			new:      `{"type": "object", "properties": {"id": {"type": "string"}}, "additionalProperties": false}`,
			breaking: []string{"ADDITIONAL_PROPERTIES_REMOVED #/additionalProperties"},
		},
		{
			name:     "optional field added to open model is breaking under strict",
			old:      `{"type": "object", "properties": {"id": {"type": "string"}}}`,
			new:      `{"type": "object", "properties": {"id": {"type": "string"}, "note": {"type": "string"}}}`,
			breaking: []string{"OPTIONAL_PROPERTY_ADDED_TO_OPEN_CONTENT_MODEL #/properties/note"},
		},
		{
			name:   "optional field added to open model is fine under lenient",
			policy: Lenient,
			old:    `{"type": "object", "properties": {"id": {"type": "string"}}}`,
			new:    `{"type": "object", "properties": {"id": {"type": "string"}, "note": {"type": "string"}}}`,
		},
		{
			name:     "oneOf branch removed",
			old:      `{"oneOf": [{"type": "string"}, {"type": "number"}]}`,
			new:      `{"oneOf": [{"type": "string"}]}`,
			breaking: []string{"SUM_TYPE_NARROWED #/"},
		},
		{
			name: "nullable via type array",
			old:  `{"type": "string"}`,
			new:  `{"type": ["string", "null"]}`,
		},
		{
			name:     "nested array item narrowed",
			old:      `{"type": "object", "properties": {"lines": {"type": "array", "items": {"type": "object", "properties": {"qty": {"type": "number"}}}}}}`,
			new:      `{"type": "object", "properties": {"lines": {"type": "array", "items": {"type": "object", "properties": {"qty": {"type": "integer"}}}}}}`,
			breaking: []string{"TYPE_NARROWED #/properties/lines/items/properties/qty"},
		},
		{
			name:     "ref target changed",
			old:      `{"$defs": {"money": {"type": "integer"}}, "type": "object", "properties": {"total": {"$ref": "#/$defs/money"}}}`,
			new:      `{"$defs": {"money": {"type": "string"}}, "type": "object", "properties": {"total": {"$ref": "#/$defs/money"}}}`,
			breaking: []string{"TYPE_CHANGED #/properties/total"},
		},
		{
			name: "recursive schema unchanged",
			old:  `{"type": "object", "properties": {"child": {"$ref": "#"}}}`,
			new:  `{"type": "object", "properties": {"child": {"$ref": "#"}}}`,
		},
		{
			name:     "dependentRequired added",
			old:      `{"type": "object", "properties": {"card": {"type": "string"}, "billing": {"type": "string"}}}`,
			new:      `{"type": "object", "properties": {"card": {"type": "string"}, "billing": {"type": "string"}}, "dependentRequired": {"card": ["billing"]}}`,
			breaking: []string{"DEPENDENCY_ARRAY_ADDED #/dependencies/card"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.policy
			if p == "" {
				p = Strict
			}
			var got []string
			for _, d := range Incompatible(Compare(mustParse(t, tt.old), mustParse(t, tt.new), p), p) {
				got = append(got, d.Type+" "+d.Path)
			}
			slices.Sort(got)
			want := slices.Clone(tt.breaking)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("breaking changes\n got  %v\n want %v", got, want)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	for _, s := range []string{
		`{`,
		`"string"`,
		`{"type": "strng"}`,
		`{"$ref": "https://example.com/schema.json"}`,
		`{"$ref": "#/definitions/missing"}`,
		`{"properties": {"a": 7}}`,
	} {
		if _, err := Parse([]byte(s)); err == nil {
			t.Errorf("Parse(%s): want error", s)
		}
	}
}

func TestIsCompatiblePolicies(t *testing.T) {
	if IsCompatible("OPTIONAL_PROPERTY_ADDED_TO_OPEN_CONTENT_MODEL", Strict) {
		t.Fatal("strict should reject optional property added to open model")
	}
	if !IsCompatible("OPTIONAL_PROPERTY_ADDED_TO_OPEN_CONTENT_MODEL", Lenient) {
		t.Fatal("lenient should allow optional property added to open model")
	}
	if IsCompatible("TYPE_CHANGED", Lenient) {
		t.Fatal("type change is never compatible")
	}
}
