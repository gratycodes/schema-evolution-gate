// Package jsonschema loads JSON Schema documents into a small structural
// model and diffs two of them the way Confluent Schema Registry decides JSON
// Schema compatibility: every change between an original (writer) and an
// update (reader) schema is classified, and the update is compatible when
// every change is in the allowed set for the chosen policy.
//
// The model follows how Confluent's everit-based loader shapes schemas
// ("type" plus "enum" becomes allOf[type, enum], "type": [a, b] becomes
// anyOf, and so on), because those shapes decide which changes are seen.
// No Confluent code is included; the behavior is cross-checked against
// Confluent's published diff examples in the test suite.
package jsonschema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Kind is the structural kind of a schema node.
type Kind int

// Schema kinds.
const (
	KEmpty Kind = iota // true or {}: accepts anything
	KFalse             // false: accepts nothing
	KObject
	KArray
	KString
	KNumber
	KBoolean
	KNull
	KEnum
	KConst
	KCombined
	KNot
)

func (k Kind) String() string {
	return [...]string{"any", "nothing", "object", "array", "string", "number", "boolean", "null", "enum", "const", "combined", "not"}[k]
}

// Schema is one loaded schema node.
type Schema struct {
	Kind Kind

	ID, Title, Description *string
	Default                any
	HasDefault             bool

	// string
	MinLength, MaxLength *float64
	Pattern              *string

	// number / integer
	Minimum, Maximum, ExclusiveMinimum, ExclusiveMaximum, MultipleOf *float64
	Integer                                                          bool

	// object
	Properties   map[string]*Schema
	Required     map[string]bool
	AddlPermits  bool    // additionalProperties is not false
	AddlSchema   *Schema // additionalProperties given as a (non-trivial) schema
	PatternProps []PatternProp
	PropDeps     map[string][]string
	SchemaDeps   map[string]*Schema
	MinProps     *float64
	MaxProps     *float64

	// array
	AllItems         *Schema   // "items": {...}
	ItemList         []*Schema // "items": [...] (tuple)
	AddlItemsPermits bool
	AddlItemsSchema  *Schema
	MinItems         *float64
	MaxItems         *float64
	Unique           bool

	// enum / const
	Enum  []string // canonical JSON of each allowed value
	Const string   // canonical JSON

	// combined (allOf / anyOf / oneOf)
	Criterion string
	Subs      []*Schema

	// not
	Not *Schema

	key string // canonical JSON of the source node, used to de-duplicate subschemas
}

// PatternProp is one patternProperties entry.
type PatternProp struct {
	Source string
	Re     *regexp.Regexp
	Schema *Schema
}

// Parse loads a JSON Schema document. Local $refs (#/definitions/..., #/$defs/...,
// #) are resolved; remote $refs are rejected.
func Parse(data []byte) (*Schema, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("json schema: invalid JSON: %w", err)
	}
	l := &loader{root: root, refs: map[string]*Schema{}}
	return l.load(root)
}

type loader struct {
	root any
	refs map[string]*Schema
}

func canonical(v any) string {
	b, _ := json.Marshal(v) // maps marshal with sorted keys
	return string(b)
}

func num(m map[string]any, key string) *float64 {
	n, ok := m[key].(json.Number)
	if !ok {
		return nil
	}
	f, err := n.Float64()
	if err != nil {
		return nil
	}
	return &f
}

func strp(m map[string]any, key string) *string {
	s, ok := m[key].(string)
	if !ok {
		return nil
	}
	return &s
}

var (
	objectKeywords = []string{"properties", "required", "additionalProperties", "patternProperties", "dependencies", "dependentRequired", "dependentSchemas", "minProperties", "maxProperties", "propertyNames"}
	arrayKeywords  = []string{"items", "additionalItems", "minItems", "maxItems", "uniqueItems", "contains"}
	stringKeywords = []string{"minLength", "maxLength", "pattern", "format"}
	numberKeywords = []string{"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf"}
)

func hasAny(m map[string]any, keys []string) bool {
	for _, k := range keys {
		if _, ok := m[k]; ok {
			return true
		}
	}
	return false
}

func (l *loader) resolveRef(ref string) (*Schema, error) {
	if s, ok := l.refs[ref]; ok {
		return s, nil
	}
	if !strings.HasPrefix(ref, "#") {
		return nil, fmt.Errorf("json schema: only local $ref is supported, got %q", ref)
	}
	var node any = l.root
	ptr := strings.TrimPrefix(ref, "#")
	if ptr != "" {
		for _, tok := range strings.Split(strings.TrimPrefix(ptr, "/"), "/") {
			tok = strings.ReplaceAll(strings.ReplaceAll(tok, "~1", "/"), "~0", "~")
			m, ok := node.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("json schema: cannot resolve $ref %q", ref)
			}
			if node, ok = m[tok]; !ok {
				return nil, fmt.Errorf("json schema: cannot resolve $ref %q", ref)
			}
		}
	}
	placeholder := &Schema{}
	l.refs[ref] = placeholder
	s, err := l.load(node)
	if err != nil {
		return nil, err
	}
	*placeholder = *s
	return placeholder, nil
}

func (l *loader) load(node any) (*Schema, error) {
	switch v := node.(type) {
	case bool:
		if v {
			return &Schema{Kind: KEmpty, key: "true"}, nil
		}
		return &Schema{Kind: KFalse, key: "false"}, nil
	case map[string]any:
		return l.loadObject(v)
	}
	return nil, fmt.Errorf("json schema: a schema must be an object or boolean, got %v", node)
}

func (l *loader) loadObject(m map[string]any) (*Schema, error) {
	if ref, ok := m["$ref"].(string); ok {
		return l.resolveRef(ref)
	}
	var parts []*Schema

	types := typeList(m["type"])
	if len(types) > 0 {
		var typed []*Schema
		for _, t := range types {
			s, err := l.typed(t, m)
			if err != nil {
				return nil, err
			}
			typed = append(typed, s)
		}
		if len(typed) == 1 {
			parts = append(parts, typed[0])
		} else {
			parts = append(parts, &Schema{Kind: KCombined, Criterion: "anyOf", Subs: typed, key: canonical(m["type"])})
		}
	} else {
		for _, fam := range []struct {
			t    string
			keys []string
		}{{"object", objectKeywords}, {"array", arrayKeywords}, {"string", stringKeywords}, {"number", numberKeywords}} {
			if hasAny(m, fam.keys) {
				s, err := l.typed(fam.t, m)
				if err != nil {
					return nil, err
				}
				parts = append(parts, s)
			}
		}
	}
	if e, ok := m["enum"].([]any); ok {
		s := &Schema{Kind: KEnum, key: canonical(map[string]any{"enum": e})}
		for _, v := range e {
			s.Enum = append(s.Enum, canonical(v))
		}
		sort.Strings(s.Enum)
		parts = append(parts, s)
	}
	if c, ok := m["const"]; ok {
		parts = append(parts, &Schema{Kind: KConst, Const: canonical(c), key: canonical(map[string]any{"const": c})})
	}
	for _, crit := range []string{"allOf", "anyOf", "oneOf"} {
		arr, ok := m[crit].([]any)
		if !ok {
			continue
		}
		s := &Schema{Kind: KCombined, Criterion: crit, key: canonical(map[string]any{crit: arr})}
		for _, sub := range arr {
			ss, err := l.load(sub)
			if err != nil {
				return nil, err
			}
			s.Subs = append(s.Subs, ss)
		}
		parts = append(parts, s)
	}
	if n, ok := m["not"]; ok {
		ns, err := l.load(n)
		if err != nil {
			return nil, err
		}
		parts = append(parts, &Schema{Kind: KNot, Not: ns, key: canonical(map[string]any{"not": n})})
	}

	var s *Schema
	switch len(parts) {
	case 0:
		s = &Schema{Kind: KEmpty}
	case 1:
		s = parts[0]
	default:
		s = &Schema{Kind: KCombined, Criterion: "allOf", Subs: parts}
	}
	s.key = canonical(m)
	s.ID = strp(m, "$id")
	if s.ID == nil {
		s.ID = strp(m, "id")
	}
	s.Title = strp(m, "title")
	s.Description = strp(m, "description")
	if d, ok := m["default"]; ok {
		s.Default, s.HasDefault = canonical(d), true
	}
	return s, nil
}

func typeList(t any) []string {
	switch v := t.(type) {
	case string:
		return []string{v}
	case []any:
		var out []string
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func (l *loader) typed(t string, m map[string]any) (*Schema, error) {
	switch t {
	case "string":
		return &Schema{Kind: KString, MinLength: num(m, "minLength"), MaxLength: num(m, "maxLength"), Pattern: strp(m, "pattern")}, nil
	case "number", "integer":
		return &Schema{
			Kind: KNumber, Integer: t == "integer",
			Minimum: num(m, "minimum"), Maximum: num(m, "maximum"),
			ExclusiveMinimum: num(m, "exclusiveMinimum"), ExclusiveMaximum: num(m, "exclusiveMaximum"),
			MultipleOf: num(m, "multipleOf"),
		}, nil
	case "boolean":
		return &Schema{Kind: KBoolean}, nil
	case "null":
		return &Schema{Kind: KNull}, nil
	case "object":
		return l.object(m)
	case "array":
		return l.array(m)
	}
	return nil, fmt.Errorf("json schema: unknown type %q", t)
}

// subschema loads an optional keyword whose value is a schema; true and {}
// mean "anything" and are reported as nil so callers can tell an open slot
// from a constrained one.
func (l *loader) subschema(m map[string]any, key string) (permits bool, schema *Schema, err error) {
	v, ok := m[key]
	if !ok {
		return true, nil, nil
	}
	switch x := v.(type) {
	case bool:
		return x, nil, nil
	case map[string]any:
		if len(x) == 0 {
			return true, nil, nil
		}
	}
	s, err := l.load(v)
	return true, s, err
}

func (l *loader) object(m map[string]any) (*Schema, error) {
	s := &Schema{Kind: KObject, Properties: map[string]*Schema{}, Required: map[string]bool{},
		PropDeps: map[string][]string{}, SchemaDeps: map[string]*Schema{},
		MinProps: num(m, "minProperties"), MaxProps: num(m, "maxProperties")}
	if props, ok := m["properties"].(map[string]any); ok {
		for name, p := range props {
			ps, err := l.load(p)
			if err != nil {
				return nil, fmt.Errorf("properties/%s: %w", name, err)
			}
			s.Properties[name] = ps
		}
	}
	for _, r := range typeList(m["required"]) {
		s.Required[r] = true
	}
	var err error
	if s.AddlPermits, s.AddlSchema, err = l.subschema(m, "additionalProperties"); err != nil {
		return nil, err
	}
	if pp, ok := m["patternProperties"].(map[string]any); ok {
		keys := make([]string, 0, len(pp))
		for k := range pp {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			ps, err := l.load(pp[k])
			if err != nil {
				return nil, err
			}
			re, _ := regexp.Compile(k) // ECMA-only syntax simply never matches
			s.PatternProps = append(s.PatternProps, PatternProp{Source: k, Re: re, Schema: ps})
		}
	}
	addDeps := func(deps map[string]any, allowArrays, allowSchemas bool) error {
		for k, d := range deps {
			if arr, ok := d.([]any); ok && allowArrays {
				for _, x := range arr {
					if str, ok := x.(string); ok {
						s.PropDeps[k] = append(s.PropDeps[k], str)
					}
				}
				sort.Strings(s.PropDeps[k])
				if s.PropDeps[k] == nil {
					s.PropDeps[k] = []string{}
				}
			} else if allowSchemas {
				ds, err := l.load(d)
				if err != nil {
					return err
				}
				s.SchemaDeps[k] = ds
			}
		}
		return nil
	}
	if d, ok := m["dependencies"].(map[string]any); ok {
		if err := addDeps(d, true, true); err != nil {
			return nil, err
		}
	}
	if d, ok := m["dependentRequired"].(map[string]any); ok {
		if err := addDeps(d, true, false); err != nil {
			return nil, err
		}
	}
	if d, ok := m["dependentSchemas"].(map[string]any); ok {
		if err := addDeps(d, false, true); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (l *loader) array(m map[string]any) (*Schema, error) {
	s := &Schema{Kind: KArray, MinItems: num(m, "minItems"), MaxItems: num(m, "maxItems")}
	s.Unique, _ = m["uniqueItems"].(bool)
	switch items := m["items"].(type) {
	case []any:
		for _, it := range items {
			is, err := l.load(it)
			if err != nil {
				return nil, err
			}
			s.ItemList = append(s.ItemList, is)
		}
	case nil:
	default:
		is, err := l.load(items)
		if err != nil {
			return nil, err
		}
		s.AllItems = is
	}
	var err error
	if s.AddlItemsPermits, s.AddlItemsSchema, err = l.subschema(m, "additionalItems"); err != nil {
		return nil, err
	}
	return s, nil
}
