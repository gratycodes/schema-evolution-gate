// Package replay validates recorded payloads against a proposed schema. Rule
// checks say what *could* break; replay shows what *does* break for the
// events you actually produce (a removed enum value that real events use, a
// tightened maxLength that real values exceed, a field that is "optional" in
// the old schema but present in every real event).
package replay

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	jsv "github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/gratycodes/schema-evolution-gate/internal/avro"
	"github.com/gratycodes/schema-evolution-gate/internal/compat"
)

// Sample is one recorded payload.
type Sample struct {
	Name string // file, or file:line for JSON Lines
	Data any
}

// Failure is one problem in one sample.
type Failure struct {
	Sample  string `json:"sample"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Result summarizes a replay.
type Result struct {
	Total  int `json:"total"`
	Failed int `json:"failed"`
	// Failures lists the problems found, capped at MaxListed.
	Failures []Failure `json:"failures"`
	// Stale counts samples that do not even match the base schema; they say
	// more about the samples (or the base schema) than about the change.
	Stale         int       `json:"stale"`
	StaleExamples []Failure `json:"staleExamples,omitempty"`
}

// MaxListed caps how many failures are kept per list.
const MaxListed = 20

// Encoding of Avro samples.
type Encoding = avro.Encoding

// LoadSamples reads *.json (one payload per file) and *.jsonl / *.ndjson
// (one payload per line) from dir, sorted by name.
func LoadSamples(dir string) ([]Sample, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("samples: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	var out []Sample
	for _, name := range names {
		path := filepath.Join(dir, name)
		switch strings.ToLower(filepath.Ext(name)) {
		case ".json":
			data, err := os.ReadFile(path)
			if err != nil {
				return nil, err
			}
			v, err := decode(data)
			if err != nil {
				return nil, fmt.Errorf("samples: %s: %w", path, err)
			}
			out = append(out, Sample{Name: name, Data: v})
		case ".jsonl", ".ndjson":
			f, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			lines, err := readLines(f, name)
			f.Close()
			if err != nil {
				return nil, err
			}
			out = append(out, lines...)
		}
	}
	return out, nil
}

func readLines(r io.Reader, name string) ([]Sample, error) {
	var out []Sample
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 16<<20)
	n := 0
	for sc.Scan() {
		n++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		v, err := decode(line)
		if err != nil {
			return nil, fmt.Errorf("samples: %s:%d: %w", name, n, err)
		}
		out = append(out, Sample{Name: fmt.Sprintf("%s:%d", name, n), Data: v})
	}
	return out, sc.Err()
}

func decode(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// Run validates samples against head. base may be nil (new subject); when
// present it is used to spot stale samples and, for Avro, as the writer
// schema the samples were produced with.
func Run(format compat.Format, base, head []byte, samples []Sample, enc Encoding) (Result, error) {
	var check func(Sample) []Failure
	var stale func(Sample) []Failure
	switch format {
	case compat.Avro:
		h, err := avro.Parse(head)
		if err != nil {
			return Result{}, err
		}
		writer := h
		if base != nil {
			if writer, err = avro.Parse(base); err != nil {
				return Result{}, err
			}
			stale = func(s Sample) []Failure { return toFailures(s.Name, avro.Validate(writer, s.Data, enc)) }
		}
		check = func(s Sample) []Failure {
			if base == nil {
				return toFailures(s.Name, avro.Validate(h, s.Data, enc))
			}
			return toFailures(s.Name, avro.Resolve(h, writer, s.Data, enc))
		}
	case compat.JSONSchema:
		h, err := compileJSON(head)
		if err != nil {
			return Result{}, err
		}
		check = func(s Sample) []Failure { return jsonFailures(s, h) }
		if base != nil {
			b, err := compileJSON(base)
			if err != nil {
				return Result{}, err
			}
			stale = func(s Sample) []Failure { return jsonFailures(s, b) }
		}
	default:
		return Result{}, fmt.Errorf("replay: unsupported format %q", format)
	}

	res := Result{Total: len(samples), Failures: []Failure{}}
	for _, s := range samples {
		if stale != nil {
			if f := stale(s); len(f) > 0 {
				res.Stale++
				if len(res.StaleExamples) < MaxListed {
					res.StaleExamples = append(res.StaleExamples, f[0])
				}
				continue // judge the change only on samples the base accepts
			}
		}
		if f := check(s); len(f) > 0 {
			res.Failed++
			for _, one := range f {
				if len(res.Failures) < MaxListed {
					res.Failures = append(res.Failures, one)
				}
			}
		}
	}
	return res, nil
}

func toFailures(name string, errs []avro.DatumError) []Failure {
	out := make([]Failure, 0, len(errs))
	for _, e := range errs {
		out = append(out, Failure{Sample: name, Path: e.Path, Message: e.Message})
	}
	return out
}

func compileJSON(schema []byte) (*jsv.Schema, error) {
	doc, err := jsv.UnmarshalJSON(bytes.NewReader(schema))
	if err != nil {
		return nil, fmt.Errorf("json schema: %w", err)
	}
	c := jsv.NewCompiler()
	c.DefaultDraft(jsv.Draft7)
	const url = "mem://schema.json"
	if err := c.AddResource(url, doc); err != nil {
		return nil, err
	}
	return c.Compile(url)
}

var printer = message.NewPrinter(language.English)

func jsonFailures(s Sample, sch *jsv.Schema) []Failure {
	// The validator wants numbers as json.Number, which is how samples are
	// decoded already.
	err := sch.Validate(s.Data)
	if err == nil {
		return nil
	}
	ve, ok := err.(*jsv.ValidationError)
	if !ok {
		return []Failure{{Sample: s.Name, Path: "/", Message: err.Error()}}
	}
	var out []Failure
	var walk func(*jsv.ValidationError)
	walk = func(e *jsv.ValidationError) {
		if len(e.Causes) == 0 {
			out = append(out, Failure{Sample: s.Name, Path: pointer(e.InstanceLocation), Message: e.ErrorKind.LocalizedString(printer)})
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	return out
}

func pointer(loc []string) string {
	if len(loc) == 0 {
		return "/"
	}
	return "/" + strings.Join(loc, "/")
}
