package avro

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// IncompatibilityType names one way a reader schema can fail to decode data
// written with a writer schema. The names match Apache Avro's
// SchemaCompatibility.SchemaIncompatibilityType, which is what Confluent
// Schema Registry reports for Avro subjects.
type IncompatibilityType string

// Incompatibility types.
const (
	NameMismatch                   IncompatibilityType = "NAME_MISMATCH"
	FixedSizeMismatch              IncompatibilityType = "FIXED_SIZE_MISMATCH"
	MissingEnumSymbols             IncompatibilityType = "MISSING_ENUM_SYMBOLS"
	ReaderFieldMissingDefaultValue IncompatibilityType = "READER_FIELD_MISSING_DEFAULT_VALUE"
	TypeMismatch                   IncompatibilityType = "TYPE_MISMATCH"
	MissingUnionBranch             IncompatibilityType = "MISSING_UNION_BRANCH"
)

// Incompatibility is one reason a reader cannot read a writer's data.
type Incompatibility struct {
	Type IncompatibilityType
	// Location is a JSON pointer into the reader schema, Avro-style
	// ("/fields/1/type/symbols").
	Location string
	// Field is a readable path through record fields ("Order.items[].sku").
	Field   string
	Message string
}

// CheckReaderWriter reports every reason data written with writer cannot be
// decoded by reader. An empty result means the pair is compatible.
func CheckReaderWriter(reader, writer *Schema) []Incompatibility {
	c := &checker{inProgress: map[[2]*Schema]bool{}}
	c.check(reader, writer, "", rootField(reader))
	return c.out
}

func rootField(s *Schema) string {
	if s.IsNamed() {
		return s.Name
	}
	return "$"
}

type checker struct {
	inProgress map[[2]*Schema]bool
	out        []Incompatibility
}

func (c *checker) add(t IncompatibilityType, loc, field, format string, args ...any) {
	if loc == "" {
		loc = "/"
	}
	c.out = append(c.out, Incompatibility{Type: t, Location: loc, Field: field, Message: fmt.Sprintf(format, args...)})
}

// compatible runs a sub-check without recording its findings.
func (c *checker) compatible(reader, writer *Schema) bool {
	sub := &checker{inProgress: c.inProgress}
	sub.check(reader, writer, "", "")
	return len(sub.out) == 0
}

func (c *checker) check(reader, writer *Schema, loc, field string) {
	key := [2]*Schema{reader, writer}
	if c.inProgress[key] {
		// Recursive schemas: assume compatible while the outer check is running.
		return
	}
	c.inProgress[key] = true
	defer delete(c.inProgress, key)

	if reader.Type == writer.Type {
		c.sameType(reader, writer, loc, field)
		return
	}
	if writer.Type == Union {
		// Every branch the writer may use must be readable.
		for i, wb := range writer.Branches {
			c.check(reader, wb, loc, field+branchSuffix(writer, i))
		}
		return
	}
	switch reader.Type {
	case Long:
		if writer.Type == Int {
			return
		}
	case Float:
		if writer.Type == Int || writer.Type == Long {
			return
		}
	case Double:
		if writer.Type == Int || writer.Type == Long || writer.Type == Float {
			return
		}
	case Bytes:
		if writer.Type == String {
			return
		}
	case String:
		if writer.Type == Bytes {
			return
		}
	case Union:
		for _, rb := range reader.Branches {
			if c.compatible(rb, writer) {
				return
			}
		}
		c.add(MissingUnionBranch, loc, field,
			"reader union %s has no branch that can read writer type %s", reader.Label(), writer.Label())
		return
	}
	c.add(TypeMismatch, loc, field, "data written as %s cannot be read as %s", writer.Label(), reader.Label())
}

func branchSuffix(u *Schema, i int) string {
	return "<" + u.Branches[i].branchName() + ">"
}

func namesMatch(reader, writer *Schema) bool {
	if reader.Name == writer.Name {
		return true
	}
	return slices.Contains(reader.Aliases, writer.FullName())
}

func (c *checker) sameType(reader, writer *Schema, loc, field string) {
	switch reader.Type {
	case Array:
		c.check(reader.Items, writer.Items, loc+"/items", field+"[]")
	case Map:
		c.check(reader.Values, writer.Values, loc+"/values", field+"{}")
	case Fixed:
		if !namesMatch(reader, writer) {
			c.add(NameMismatch, loc+"/name", field, "writer fixed %s does not match reader fixed %s by name or alias", writer.FullName(), reader.FullName())
		}
		if reader.Size != writer.Size {
			c.add(FixedSizeMismatch, loc+"/size", field, "writer fixed %s is %d bytes, reader expects %d", reader.FullName(), writer.Size, reader.Size)
		}
	case Enum:
		if !namesMatch(reader, writer) {
			c.add(NameMismatch, loc+"/name", field, "writer enum %s does not match reader enum %s by name or alias", writer.FullName(), reader.FullName())
		}
		var missing []string
		for _, sym := range writer.Symbols {
			if !slices.Contains(reader.Symbols, sym) {
				missing = append(missing, sym)
			}
		}
		if len(missing) > 0 && reader.EnumDefault == nil {
			c.add(MissingEnumSymbols, loc+"/symbols", field,
				"reader enum %s has no symbol(s) [%s] that the writer can produce, and no enum default",
				reader.FullName(), strings.Join(missing, ", "))
		}
	case Record:
		if !namesMatch(reader, writer) {
			c.add(NameMismatch, loc+"/name", field, "writer record %s does not match reader record %s by name or alias", writer.FullName(), reader.FullName())
		}
		for i, rf := range reader.Fields {
			floc := loc + "/fields/" + strconv.Itoa(i)
			fpath := field + "." + rf.Name
			wf := lookupField(writer, rf)
			if wf == nil {
				if !rf.HasDefault {
					c.add(ReaderFieldMissingDefaultValue, floc, fpath,
						"reader field %q is absent from writer data and has no default", rf.Name)
				}
				continue
			}
			c.check(rf.Type, wf.Type, floc+"/type", fpath)
		}
	case Union:
		for i, wb := range writer.Branches {
			readable := false
			for _, rb := range reader.Branches {
				if c.compatible(rb, wb) {
					readable = true
					break
				}
			}
			if !readable {
				c.add(MissingUnionBranch, loc+"/"+strconv.Itoa(i), field,
					"writer union branch %s has no matching reader branch", wb.Label())
			}
		}
	}
	// Primitives of the same type always match.
}

func lookupField(writer *Schema, rf *Field) *Field {
	for _, wf := range writer.Fields {
		if wf.Name == rf.Name {
			return wf
		}
	}
	for _, alias := range rf.Aliases {
		for _, wf := range writer.Fields {
			if wf.Name == alias {
				return wf
			}
		}
	}
	return nil
}
