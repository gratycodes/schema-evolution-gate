package avro

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"unicode/utf8"
)

// DatumError is a problem with one value inside a sample payload.
type DatumError struct {
	Path    string // JSON-pointer-like path into the payload ("/items/0/sku")
	Message string
}

func (e DatumError) Error() string { return e.Path + ": " + e.Message }

// Encoding selects how sample payloads represent Avro data.
type Encoding int

const (
	// AvroJSON is Avro's JSON encoding: every field present, non-null union
	// values wrapped as {"<branch>": value}.
	AvroJSON Encoding = iota
	// PlainJSON is what most services log: unions are not wrapped and fields
	// that have a default may be omitted.
	PlainJSON
)

// Validate checks that datum is a valid value of schema s.
func Validate(s *Schema, datum any, enc Encoding) []DatumError {
	v := &datumWalker{enc: enc}
	v.validate(s, datum, "")
	return v.errs
}

// Resolve checks that datum, written with the writer schema, can be read by
// the reader schema. This is the payload-level version of CheckReaderWriter:
// it only fails on the parts of the schema the sample actually exercises
// (for example a removed enum symbol the sample really uses).
func Resolve(reader, writer *Schema, datum any, enc Encoding) []DatumError {
	v := &datumWalker{enc: enc}
	v.resolve(reader, writer, datum, "")
	return v.errs
}

type datumWalker struct {
	enc  Encoding
	errs []DatumError
}

func (v *datumWalker) fail(path, format string, args ...any) {
	if path == "" {
		path = "/"
	}
	v.errs = append(v.errs, DatumError{Path: path, Message: fmt.Sprintf(format, args...)})
}

func ok(s *Schema, d any, enc Encoding) bool {
	return len(Validate(s, d, enc)) == 0
}

// unionBranch finds which branch of union u the datum uses.
func unionBranch(u *Schema, d any, enc Encoding) (*Schema, any, bool) {
	if d == nil {
		for _, b := range u.Branches {
			if b.Type == Null {
				return b, nil, true
			}
		}
		return nil, nil, false
	}
	if m, isMap := d.(map[string]any); isMap && len(m) == 1 {
		for k, val := range m {
			for _, b := range u.Branches {
				if b.branchName() == k || (b.IsNamed() && b.Name == k) {
					return b, val, true
				}
			}
		}
	}
	if enc == PlainJSON {
		for _, b := range u.Branches {
			if b.Type != Null && ok(b, d, enc) {
				return b, d, true
			}
		}
	}
	return nil, nil, false
}

func isInteger(n json.Number, bits int) bool {
	i, err := n.Int64()
	if err != nil {
		return false
	}
	if bits == 32 {
		return i >= math.MinInt32 && i <= math.MaxInt32
	}
	return true
}

func (v *datumWalker) validate(s *Schema, d any, path string) {
	switch s.Type {
	case Null:
		if d != nil {
			v.fail(path, "expected null, got %s", describe(d))
		}
	case Boolean:
		if _, isBool := d.(bool); !isBool {
			v.fail(path, "expected boolean, got %s", describe(d))
		}
	case Int, Long:
		n, isNum := d.(json.Number)
		bits := 64
		if s.Type == Int {
			bits = 32
		}
		if !isNum || !isInteger(n, bits) {
			v.fail(path, "expected %s, got %s", s.Type, describe(d))
		}
	case Float, Double:
		if _, isNum := d.(json.Number); !isNum {
			v.fail(path, "expected %s, got %s", s.Type, describe(d))
		}
	case String, Bytes:
		if _, isStr := d.(string); !isStr {
			v.fail(path, "expected %s, got %s", s.Type, describe(d))
		}
	case Fixed:
		str, isStr := d.(string)
		if !isStr || utf8.RuneCountInString(str) != s.Size {
			v.fail(path, "expected fixed %s of %d bytes, got %s", s.FullName(), s.Size, describe(d))
		}
	case Enum:
		str, isStr := d.(string)
		if !isStr || !slices.Contains(s.Symbols, str) {
			v.fail(path, "expected one of %s symbols %v, got %s", s.FullName(), s.Symbols, describe(d))
		}
	case Array:
		arr, isArr := d.([]any)
		if !isArr {
			v.fail(path, "expected array, got %s", describe(d))
			return
		}
		for i, item := range arr {
			v.validate(s.Items, item, fmt.Sprintf("%s/%d", path, i))
		}
	case Map:
		m, isMap := d.(map[string]any)
		if !isMap {
			v.fail(path, "expected map, got %s", describe(d))
			return
		}
		for _, k := range sortedKeys(m) {
			v.validate(s.Values, m[k], path+"/"+k)
		}
	case Record:
		m, isMap := d.(map[string]any)
		if !isMap {
			v.fail(path, "expected record %s, got %s", s.FullName(), describe(d))
			return
		}
		for _, f := range s.Fields {
			val, present := m[f.Name]
			if !present {
				if v.enc == PlainJSON && (f.HasDefault || nullable(f.Type)) {
					continue
				}
				v.fail(path+"/"+f.Name, "required field %q is missing", f.Name)
				continue
			}
			v.validate(f.Type, val, path+"/"+f.Name)
		}
	case Union:
		b, val, found := unionBranch(s, d, v.enc)
		if !found {
			v.fail(path, "value %s matches no branch of %s", describe(d), s.Label())
			return
		}
		v.validate(b, val, path)
	}
}

func nullable(s *Schema) bool {
	if s.Type == Null {
		return true
	}
	if s.Type == Union {
		for _, b := range s.Branches {
			if b.Type == Null {
				return true
			}
		}
	}
	return false
}

func (v *datumWalker) resolve(reader, writer *Schema, d any, path string) {
	if writer.Type == Union {
		wb, val, found := unionBranch(writer, d, v.enc)
		if !found {
			v.fail(path, "value %s matches no branch of writer %s", describe(d), writer.Label())
			return
		}
		v.resolve(reader, wb, val, path)
		return
	}
	if reader.Type == Union {
		for _, rb := range reader.Branches {
			if len(CheckReaderWriter(rb, writer)) == 0 {
				v.resolve(rb, writer, d, path)
				return
			}
		}
		v.fail(path, "no branch of reader %s can read a %s value", reader.Label(), writer.Label())
		return
	}
	if reader.Type != writer.Type {
		if promotable(reader.Type, writer.Type) {
			return
		}
		v.fail(path, "value written as %s cannot be read as %s", writer.Label(), reader.Label())
		return
	}
	switch reader.Type {
	case Enum:
		sym, _ := d.(string)
		if !slices.Contains(reader.Symbols, sym) && reader.EnumDefault == nil {
			v.fail(path, "enum symbol %q no longer exists in %s", sym, reader.FullName())
		}
	case Fixed:
		if reader.Size != writer.Size {
			v.fail(path, "fixed size changed from %d to %d", writer.Size, reader.Size)
		}
	case Array:
		arr, _ := d.([]any)
		for i, item := range arr {
			v.resolve(reader.Items, writer.Items, item, fmt.Sprintf("%s/%d", path, i))
		}
	case Map:
		m, _ := d.(map[string]any)
		for _, k := range sortedKeys(m) {
			v.resolve(reader.Values, writer.Values, m[k], path+"/"+k)
		}
	case Record:
		m, _ := d.(map[string]any)
		for _, rf := range reader.Fields {
			wf := lookupField(writer, rf)
			if wf == nil {
				if !rf.HasDefault {
					v.fail(path+"/"+rf.Name, "field %q is not in the payload and has no default", rf.Name)
				}
				continue
			}
			val, present := m[wf.Name]
			if !present {
				// Plain JSON omitted a defaulted writer field; nothing to resolve.
				continue
			}
			v.resolve(rf.Type, wf.Type, val, path+"/"+rf.Name)
		}
	}
}

func promotable(reader, writer Type) bool {
	switch reader {
	case Long:
		return writer == Int
	case Float:
		return writer == Int || writer == Long
	case Double:
		return writer == Int || writer == Long || writer == Float
	case Bytes:
		return writer == String
	case String:
		return writer == Bytes
	}
	return false
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func describe(d any) string {
	switch x := d.(type) {
	case nil:
		return "null"
	case bool:
		return fmt.Sprintf("boolean %v", x)
	case json.Number:
		return "number " + x.String()
	case string:
		if len(x) > 40 {
			x = x[:40] + "…"
		}
		return fmt.Sprintf("string %q", x)
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", d)
}
