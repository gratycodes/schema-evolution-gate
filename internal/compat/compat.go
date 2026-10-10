// Package compat applies Confluent Schema Registry compatibility modes
// (BACKWARD, FORWARD, FULL and their _TRANSITIVE variants) to a candidate
// schema and its previous versions, for Avro and JSON Schema.
package compat

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gratycodes/schema-evolution-gate/internal/avro"
	"github.com/gratycodes/schema-evolution-gate/internal/jsonschema"
)

// Format is a schema language.
type Format string

// Supported formats.
const (
	Avro       Format = "avro"
	JSONSchema Format = "json"
)

// FormatFromPath guesses the format from a file extension.
func FormatFromPath(path string) (Format, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".avsc":
		return Avro, nil
	case ".json":
		if strings.HasSuffix(strings.ToLower(path), ".avsc.json") {
			return Avro, nil
		}
		return JSONSchema, nil
	}
	return "", fmt.Errorf("cannot infer schema format from %q; set format: avro or json", path)
}

// ParseFormat validates a format name.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(s) {
	case "avro":
		return Avro, nil
	case "json", "jsonschema", "json-schema", "json_schema":
		return JSONSchema, nil
	}
	return "", fmt.Errorf("unsupported format %q (want avro or json; for Protobuf use buf breaking)", s)
}

// Mode is a Confluent compatibility type.
type Mode string

// Compatibility modes.
const (
	None               Mode = "NONE"
	Backward           Mode = "BACKWARD"
	BackwardTransitive Mode = "BACKWARD_TRANSITIVE"
	Forward            Mode = "FORWARD"
	ForwardTransitive  Mode = "FORWARD_TRANSITIVE"
	Full               Mode = "FULL"
	FullTransitive     Mode = "FULL_TRANSITIVE"
)

// ParseMode validates a mode name (case-insensitive).
func ParseMode(s string) (Mode, error) {
	m := Mode(strings.ToUpper(strings.TrimSpace(s)))
	switch m {
	case None, Backward, BackwardTransitive, Forward, ForwardTransitive, Full, FullTransitive:
		return m, nil
	case "":
		return Backward, nil // Confluent's default
	}
	return "", fmt.Errorf("unknown compatibility mode %q", s)
}

func (m Mode) backward() bool {
	return m == Backward || m == BackwardTransitive || m == Full || m == FullTransitive
}

func (m Mode) forward() bool {
	return m == Forward || m == ForwardTransitive || m == Full || m == FullTransitive
}

// Transitive reports whether the mode checks every previous version.
func (m Mode) Transitive() bool { return strings.HasSuffix(string(m), "_TRANSITIVE") }

// Direction says which side of the contract breaks.
type Direction string

// Directions.
const (
	// DirBackward: consumers on the new schema reading events written with an
	// older one.
	DirBackward Direction = "backward"
	// DirForward: consumers still on an older schema reading events written
	// with the new one.
	DirForward Direction = "forward"
)

// Version is one schema version: a label for reports and its source text.
type Version struct {
	Label string
	Data  []byte
}

// Options configure a check.
type Options struct {
	Format     Format
	Mode       Mode
	JSONPolicy jsonschema.Policy
}

// Issue is one breaking change.
type Issue struct {
	Rule      string    `json:"rule"`
	Direction Direction `json:"direction"`
	Against   string    `json:"against"`
	Path      string    `json:"path"`
	Field     string    `json:"field"`
	Reason    string    `json:"reason"`
	Detail    string    `json:"detail,omitempty"`
}

// Result is the outcome of checking one candidate.
type Result struct {
	Compatible bool     `json:"compatible"`
	Compared   []string `json:"compared"`
	Issues     []Issue  `json:"issues"`
	// Changes counts non-breaking differences seen (JSON Schema only).
	Changes int `json:"nonBreakingChanges"`
}

// Parse checks that a document is a valid schema of the given format.
func Parse(f Format, data []byte) (any, error) {
	switch f {
	case Avro:
		return avro.Parse(data)
	case JSONSchema:
		return jsonschema.Parse(data)
	}
	return nil, fmt.Errorf("unsupported format %q", f)
}

// Check compares candidate against history (oldest first) under opts.Mode.
// Non-transitive modes only use the last history entry.
func Check(opts Options, history []Version, candidate Version) (Result, error) {
	res := Result{Compatible: true, Issues: []Issue{}, Compared: []string{}}
	if opts.Mode == None || len(history) == 0 {
		return res, nil
	}
	if opts.JSONPolicy == "" {
		opts.JSONPolicy = jsonschema.Strict
	}
	cand, err := Parse(opts.Format, candidate.Data)
	if err != nil {
		return res, fmt.Errorf("%s: %w", candidate.Label, err)
	}
	against := history
	if !opts.Mode.Transitive() {
		against = history[len(history)-1:]
	}
	for i := len(against) - 1; i >= 0; i-- { // newest first in reports
		prev := against[i]
		old, err := Parse(opts.Format, prev.Data)
		if err != nil {
			return res, fmt.Errorf("%s: %w", prev.Label, err)
		}
		res.Compared = append(res.Compared, prev.Label)
		if opts.Mode.backward() {
			issues, changes := pair(opts, cand, old, DirBackward, prev.Label)
			res.Issues = append(res.Issues, issues...)
			res.Changes += changes
		}
		if opts.Mode.forward() {
			issues, _ := pair(opts, old, cand, DirForward, prev.Label)
			res.Issues = append(res.Issues, issues...)
		}
	}
	res.Compatible = len(res.Issues) == 0
	return res, nil
}

// pair checks that reader can read data written with writer.
func pair(opts Options, reader, writer any, dir Direction, against string) ([]Issue, int) {
	var out []Issue
	switch opts.Format {
	case Avro:
		for _, inc := range avro.CheckReaderWriter(reader.(*avro.Schema), writer.(*avro.Schema)) {
			out = append(out, Issue{
				Rule: string(inc.Type), Direction: dir, Against: against,
				Path: inc.Location, Field: inc.Field,
				Reason: explainAvro(inc, dir), Detail: inc.Message,
			})
		}
		return out, 0
	case JSONSchema:
		diffs := jsonschema.Compare(writer.(*jsonschema.Schema), reader.(*jsonschema.Schema), opts.JSONPolicy)
		bad := jsonschema.Incompatible(diffs, opts.JSONPolicy)
		for _, d := range bad {
			field := FieldFromPointer(d.Path)
			out = append(out, Issue{
				Rule: d.Type, Direction: dir, Against: against,
				Path: d.Path, Field: field,
				Reason: explainJSON(d.Type, field, dir, opts.JSONPolicy),
			})
		}
		return out, len(diffs) - len(bad)
	}
	return nil, 0
}

// FieldFromPointer turns "#/properties/items/items/properties/sku" into
// "items[].sku".
func FieldFromPointer(ptr string) string {
	segs := strings.Split(strings.TrimPrefix(strings.TrimPrefix(ptr, "#"), "/"), "/")
	var b strings.Builder
	for i := 0; i < len(segs); i++ {
		s := segs[i]
		switch s {
		case "properties", "required", "dependencies":
			if i+1 < len(segs) {
				i++
				name := strings.ReplaceAll(strings.ReplaceAll(segs[i], "~1", "/"), "~0", "~")
				if b.Len() > 0 {
					b.WriteByte('.')
				}
				b.WriteString(name)
			}
		case "items":
			b.WriteString("[]")
			if i+1 < len(segs) && isIndex(segs[i+1]) {
				i++
			}
		}
	}
	if b.Len() == 0 {
		return "(root)"
	}
	return b.String()
}

func isIndex(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
