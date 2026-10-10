package compat

import (
	"fmt"
	"strings"

	"github.com/gratycodes/schema-evolution-gate/internal/avro"
	"github.com/gratycodes/schema-evolution-gate/internal/jsonschema"
)

func avroField(f string) string {
	// "Order.items[].sku" -> "items[].sku"; keep the root name if that is all there is.
	if i := strings.IndexByte(f, '.'); i >= 0 {
		return f[i+1:]
	}
	return f
}

func explainAvro(inc avro.Incompatibility, dir Direction) string {
	f := "`" + avroField(inc.Field) + "`"
	back := dir == DirBackward
	switch inc.Type {
	case avro.ReaderFieldMissingDefaultValue:
		if back {
			return fmt.Sprintf("new field %s has no default, so the new schema cannot read older events that lack it", f)
		}
		return fmt.Sprintf("field %s was removed or renamed and had no default, so consumers on the old schema cannot read new events", f)
	case avro.MissingEnumSymbols:
		if back {
			return fmt.Sprintf("enum symbol removed from %s; older events that use it cannot be read", f)
		}
		return fmt.Sprintf("enum symbol added to %s: consumers on the old schema cannot decode it (give the old enum a default first)", f)
	case avro.MissingUnionBranch:
		if back {
			return fmt.Sprintf("union branch removed from %s; older events that use it cannot be read", f)
		}
		return fmt.Sprintf("union branch added to %s: consumers on the old schema cannot decode it", f)
	case avro.TypeMismatch:
		return fmt.Sprintf("type of %s changed: %s", f, inc.Message)
	case avro.NameMismatch:
		return fmt.Sprintf("%s renamed without an alias: %s", f, inc.Message)
	case avro.FixedSizeMismatch:
		return fmt.Sprintf("fixed size of %s changed: %s", f, inc.Message)
	}
	return inc.Message
}

var tightened = map[string]string{
	"MAX_LENGTH_DECREASED": "maxLength", "MAX_LENGTH_ADDED": "maxLength",
	"MIN_LENGTH_INCREASED": "minLength", "MIN_LENGTH_ADDED": "minLength",
	"PATTERN_ADDED": "pattern", "PATTERN_CHANGED": "pattern",
	"MAXIMUM_DECREASED": "maximum", "MAXIMUM_ADDED": "maximum",
	"MINIMUM_INCREASED": "minimum", "MINIMUM_ADDED": "minimum",
	"EXCLUSIVE_MAXIMUM_DECREASED": "exclusiveMaximum", "EXCLUSIVE_MAXIMUM_ADDED": "exclusiveMaximum",
	"EXCLUSIVE_MINIMUM_INCREASED": "exclusiveMinimum", "EXCLUSIVE_MINIMUM_ADDED": "exclusiveMinimum",
	"MULTIPLE_OF_ADDED": "multipleOf", "MULTIPLE_OF_EXPANDED": "multipleOf", "MULTIPLE_OF_CHANGED": "multipleOf",
	"MAX_ITEMS_DECREASED": "maxItems", "MAX_ITEMS_ADDED": "maxItems",
	"MIN_ITEMS_INCREASED": "minItems", "MIN_ITEMS_ADDED": "minItems",
	"UNIQUE_ITEMS_ADDED":       "uniqueItems",
	"MAX_PROPERTIES_DECREASED": "maxProperties", "MAX_PROPERTIES_ADDED": "maxProperties",
	"MIN_PROPERTIES_INCREASED": "minProperties", "MIN_PROPERTIES_ADDED": "minProperties",
}

func explainJSON(rule, field string, dir Direction, policy jsonschema.Policy) string {
	reader, writer := "the new schema", "older events"
	if dir == DirForward {
		reader, writer = "the old schema", "new events"
	}
	f := "`" + field + "`"
	if kw, ok := tightened[rule]; ok {
		return fmt.Sprintf("%s tightens %s on %s; %s may violate it", reader, kw, f, writer)
	}
	back := dir == DirBackward
	switch rule {
	case "REQUIRED_ATTRIBUTE_ADDED":
		if back {
			return fmt.Sprintf("the new schema makes %s required, but older events may omit it", f)
		}
		return fmt.Sprintf("%s is no longer required, but the old schema requires it; new events may omit it", f)
	case "REQUIRED_PROPERTY_ADDED_TO_OPEN_CONTENT_MODEL", "REQUIRED_PROPERTY_ADDED_TO_UNOPEN_CONTENT_MODEL":
		if back {
			return fmt.Sprintf("the new schema adds required property %s with no default; older events do not have it", f)
		}
		return fmt.Sprintf("%s was removed, but the old schema requires it; new events will not have it", f)
	case "REQUIRED_PROPERTY_WITH_DEFAULT_ADDED_TO_OPEN_CONTENT_MODEL", "OPTIONAL_PROPERTY_ADDED_TO_OPEN_CONTENT_MODEL":
		var msg string
		if back {
			msg = fmt.Sprintf("the new schema declares %s, but the old schema is an open content model, so older events may already carry %s with any type", f, f)
		} else {
			msg = fmt.Sprintf("%s was removed from an open content model; the old schema still declares it, and new events may carry %s with any type", f, f)
		}
		if policy == jsonschema.Strict {
			msg += " (Confluent STRICT policy; set additionalProperties: false, or use jsonPolicy: lenient)"
		}
		return msg
	case "PROPERTY_REMOVED_FROM_CLOSED_CONTENT_MODEL":
		if back {
			return fmt.Sprintf("the new schema drops %s and forbids unknown properties, so older events that include it are rejected", f)
		}
		return fmt.Sprintf("%s is new, but the old schema forbids unknown properties (additionalProperties: false), so new events that include it are rejected", f)
	case "PROPERTY_ADDED_NOT_COVERED_BY_PARTIALLY_OPEN_CONTENT_MODEL", "PROPERTY_REMOVED_NOT_COVERED_BY_PARTIALLY_OPEN_CONTENT_MODEL":
		return fmt.Sprintf("%s does not fit the additionalProperties/patternProperties schema of the other version", f)
	case "TYPE_CHANGED":
		return fmt.Sprintf("type of %s changed; %s no longer validate against %s", f, writer, reader)
	case "TYPE_NARROWED":
		return fmt.Sprintf("%s narrowed from number to integer; %s may contain decimals", f, writer)
	case "ENUM_ARRAY_NARROWED", "ENUM_ARRAY_CHANGED":
		return fmt.Sprintf("%s no longer allows some enum/const values of %s that %s may use", reader, f, writer)
	case "ADDITIONAL_PROPERTIES_REMOVED", "ADDITIONAL_PROPERTIES_NARROWED":
		return fmt.Sprintf("%s restricts additional properties on %s; %s with extra fields are rejected", reader, f, writer)
	case "ADDITIONAL_ITEMS_REMOVED", "ADDITIONAL_ITEMS_NARROWED":
		return fmt.Sprintf("%s restricts additional array items on %s", reader, f)
	case "SUM_TYPE_NARROWED":
		return fmt.Sprintf("%s removed a oneOf/anyOf branch of %s that %s may use", reader, f, writer)
	case "PRODUCT_TYPE_EXTENDED":
		return fmt.Sprintf("%s adds an allOf constraint to %s", reader, f)
	case "COMBINED_TYPE_CHANGED", "COMBINED_TYPE_SUBSCHEMAS_CHANGED":
		return fmt.Sprintf("the combined constraints on %s (type plus enum/const, or allOf/anyOf/oneOf) changed so %s may no longer match", f, writer)
	case "SCHEMA_ADDED":
		return fmt.Sprintf("%s adds a schema for %s where there was none", reader, f)
	case "NOT_TYPE_EXTENDED":
		return fmt.Sprintf("%s forbids more values of %s via not", reader, f)
	case "DEPENDENCY_ARRAY_ADDED", "DEPENDENCY_ARRAY_EXTENDED", "DEPENDENCY_ARRAY_CHANGED", "DEPENDENCY_SCHEMA_ADDED":
		return fmt.Sprintf("%s adds or changes a dependency on %s", reader, f)
	case "ITEM_REMOVED_FROM_CLOSED_CONTENT_MODEL", "ITEM_ADDED_TO_OPEN_CONTENT_MODEL",
		"ITEM_ADDED_NOT_COVERED_BY_PARTIALLY_OPEN_CONTENT_MODEL", "ITEM_REMOVED_NOT_COVERED_BY_PARTIALLY_OPEN_CONTENT_MODEL":
		return fmt.Sprintf("tuple item %s changed so %s may no longer match", f, writer)
	}
	return fmt.Sprintf("incompatible change (%s) at %s", rule, f)
}
