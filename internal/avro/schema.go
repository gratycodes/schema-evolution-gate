// Package avro parses Avro schemas (the JSON form, .avsc) and checks
// reader/writer compatibility following the Avro specification's schema
// resolution rules, the same rules Confluent Schema Registry applies to Avro
// subjects through Apache Avro's SchemaCompatibility.
package avro

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Type is an Avro schema type.
type Type string

// Avro primitive and complex types.
const (
	Null    Type = "null"
	Boolean Type = "boolean"
	Int     Type = "int"
	Long    Type = "long"
	Float   Type = "float"
	Double  Type = "double"
	Bytes   Type = "bytes"
	String  Type = "string"
	Record  Type = "record"
	Enum    Type = "enum"
	Array   Type = "array"
	Map     Type = "map"
	Fixed   Type = "fixed"
	Union   Type = "union"
)

var primitives = map[string]Type{
	"null": Null, "boolean": Boolean, "int": Int, "long": Long,
	"float": Float, "double": Double, "bytes": Bytes, "string": String,
}

// Schema is a parsed Avro schema node. Named types (record, enum, fixed) are
// shared by pointer wherever they are referenced, so recursive schemas form
// cycles.
type Schema struct {
	Type        Type
	Name        string // unqualified name for named types
	Namespace   string
	Aliases     []string // full names
	Fields      []*Field
	Symbols     []string
	EnumDefault *string
	Items       *Schema
	Values      *Schema
	Size        int
	Branches    []*Schema
	LogicalType string
}

// Field is a record field.
type Field struct {
	Name       string
	Type       *Schema
	Default    json.RawMessage
	HasDefault bool
	Aliases    []string
}

// FullName returns namespace.name for named types.
func (s *Schema) FullName() string {
	if s.Namespace == "" {
		return s.Name
	}
	return s.Namespace + "." + s.Name
}

// IsNamed reports whether the schema is a record, enum or fixed.
func (s *Schema) IsNamed() bool {
	return s.Type == Record || s.Type == Enum || s.Type == Fixed
}

// Label is a short human description used in messages: "string",
// "record Order", "union [null, string]".
func (s *Schema) Label() string {
	switch {
	case s.IsNamed():
		return string(s.Type) + " " + s.FullName()
	case s.Type == Array:
		return "array<" + s.Items.Label() + ">"
	case s.Type == Map:
		return "map<" + s.Values.Label() + ">"
	case s.Type == Union:
		parts := make([]string, len(s.Branches))
		for i, b := range s.Branches {
			parts[i] = b.Label()
		}
		return "union [" + strings.Join(parts, ", ") + "]"
	}
	return string(s.Type)
}

// branchName is the name a union branch is keyed by in Avro's JSON encoding.
func (s *Schema) branchName() string {
	if s.IsNamed() {
		return s.FullName()
	}
	return string(s.Type)
}

// Parse parses an Avro schema document.
func Parse(data []byte) (*Schema, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("avro: invalid JSON: %w", err)
	}
	p := &parser{named: map[string]*Schema{}}
	return p.parse(raw, "")
}

type parser struct {
	named map[string]*Schema
}

func qualify(name, namespace string) (short, ns string) {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:], name[:i]
	}
	return name, namespace
}

func (p *parser) parse(raw any, namespace string) (*Schema, error) {
	switch v := raw.(type) {
	case string:
		if t, ok := primitives[v]; ok {
			return &Schema{Type: t}, nil
		}
		short, ns := qualify(v, namespace)
		full := short
		if ns != "" {
			full = ns + "." + short
		}
		if s, ok := p.named[full]; ok {
			return s, nil
		}
		// Unqualified reference to a type defined in the null namespace.
		if s, ok := p.named[v]; ok {
			return s, nil
		}
		return nil, fmt.Errorf("avro: unknown type %q", v)
	case []any:
		u := &Schema{Type: Union}
		seen := map[string]bool{}
		for _, b := range v {
			bs, err := p.parse(b, namespace)
			if err != nil {
				return nil, err
			}
			if bs.Type == Union {
				return nil, fmt.Errorf("avro: unions may not immediately contain other unions")
			}
			key := bs.branchName()
			if seen[key] {
				return nil, fmt.Errorf("avro: duplicate %q in union", key)
			}
			seen[key] = true
			u.Branches = append(u.Branches, bs)
		}
		return u, nil
	case map[string]any:
		return p.parseObject(v, namespace)
	}
	return nil, fmt.Errorf("avro: unexpected schema value %v", raw)
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func strList(m map[string]any, key string) []string {
	arr, _ := m[key].([]any)
	out := make([]string, 0, len(arr))
	for _, a := range arr {
		if s, ok := a.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func (p *parser) parseObject(m map[string]any, namespace string) (*Schema, error) {
	typ, ok := m["type"]
	if !ok {
		return nil, fmt.Errorf("avro: schema object without \"type\"")
	}
	tname, isString := typ.(string)
	if !isString {
		// {"type": {...}} or {"type": [...]} wraps another schema.
		return p.parse(typ, namespace)
	}
	logical := str(m, "logicalType")
	if t, ok := primitives[tname]; ok {
		return &Schema{Type: t, LogicalType: logical}, nil
	}
	switch Type(tname) {
	case Record, "error", Enum, Fixed:
		name := str(m, "name")
		if name == "" {
			return nil, fmt.Errorf("avro: %s without a name", tname)
		}
		ns := namespace
		if v, ok := m["namespace"].(string); ok {
			ns = v
		}
		short, ns := qualify(name, ns)
		s := &Schema{Type: Type(tname), Name: short, Namespace: ns, LogicalType: logical}
		if s.Type == "error" {
			s.Type = Record
		}
		for _, a := range strList(m, "aliases") {
			as, ans := qualify(a, ns)
			if ans != "" {
				as = ans + "." + as
			}
			s.Aliases = append(s.Aliases, as)
		}
		if _, dup := p.named[s.FullName()]; dup {
			return nil, fmt.Errorf("avro: type %q defined twice", s.FullName())
		}
		p.named[s.FullName()] = s
		switch s.Type {
		case Record:
			fields, _ := m["fields"].([]any)
			names := map[string]bool{}
			for i, f := range fields {
				fm, ok := f.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("avro: %s field %d is not an object", s.FullName(), i)
				}
				fname := str(fm, "name")
				if fname == "" {
					return nil, fmt.Errorf("avro: %s field %d has no name", s.FullName(), i)
				}
				if names[fname] {
					return nil, fmt.Errorf("avro: %s has duplicate field %q", s.FullName(), fname)
				}
				names[fname] = true
				ft, err := p.parse(fm["type"], ns)
				if err != nil {
					return nil, fmt.Errorf("%s.%s: %w", s.FullName(), fname, err)
				}
				field := &Field{Name: fname, Type: ft, Aliases: strList(fm, "aliases")}
				if d, ok := fm["default"]; ok {
					b, _ := json.Marshal(d)
					field.Default = b
					field.HasDefault = true
				}
				s.Fields = append(s.Fields, field)
			}
		case Enum:
			s.Symbols = strList(m, "symbols")
			if d, ok := m["default"].(string); ok {
				s.EnumDefault = &d
			}
		case Fixed:
			n, ok := m["size"].(json.Number)
			if !ok {
				return nil, fmt.Errorf("avro: fixed %s without size", s.FullName())
			}
			size, err := n.Int64()
			if err != nil {
				return nil, fmt.Errorf("avro: fixed %s: bad size %v", s.FullName(), n)
			}
			s.Size = int(size)
		}
		return s, nil
	case Array:
		items, err := p.parse(m["items"], namespace)
		if err != nil {
			return nil, err
		}
		return &Schema{Type: Array, Items: items, LogicalType: logical}, nil
	case Map:
		values, err := p.parse(m["values"], namespace)
		if err != nil {
			return nil, err
		}
		return &Schema{Type: Map, Values: values, LogicalType: logical}, nil
	}
	// A named reference written as {"type": "com.acme.Money"}.
	return p.parse(tname, namespace)
}
