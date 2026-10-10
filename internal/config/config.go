// Package config loads .schema-gate.yaml, which maps Kafka topics to schema
// files, compatibility modes, version history and recorded samples.
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"

	"github.com/gratycodes/schema-evolution-gate/internal/avro"
	"github.com/gratycodes/schema-evolution-gate/internal/compat"
	"github.com/gratycodes/schema-evolution-gate/internal/jsonschema"
)

// DefaultFile is the config file name looked up when none is given.
const DefaultFile = ".schema-gate.yaml"

// Topic is one subject to guard.
type Topic struct {
	Topic          string `yaml:"topic"`
	Subject        string `yaml:"subject"`
	Schema         string `yaml:"schema"`
	Format         string `yaml:"format"`
	Mode           string `yaml:"mode"`
	JSONPolicy     string `yaml:"jsonPolicy"`
	History        string `yaml:"history"`
	Samples        string `yaml:"samples"`
	SampleEncoding string `yaml:"sampleEncoding"`

	// Resolved values.
	ResolvedFormat   compat.Format     `yaml:"-"`
	ResolvedMode     compat.Mode       `yaml:"-"`
	ResolvedPolicy   jsonschema.Policy `yaml:"-"`
	ResolvedEncoding avro.Encoding     `yaml:"-"`
}

// Defaults apply to every topic that does not override them.
type Defaults struct {
	Mode           string `yaml:"mode"`
	JSONPolicy     string `yaml:"jsonPolicy"`
	SampleEncoding string `yaml:"sampleEncoding"`
}

// Config is the whole file.
type Config struct {
	Version  int      `yaml:"version"`
	Defaults Defaults `yaml:"defaults"`
	Topics   []Topic  `yaml:"topics"`

	// Dir is the directory paths are relative to (the config file's).
	Dir string `yaml:"-"`
}

// Load reads and validates a config file.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	return Parse(data, filepath.Dir(abs))
}

// Parse validates config text; dir anchors relative paths.
func Parse(data []byte, dir string) (*Config, error) {
	var c Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	c.Dir = dir
	if c.Version != 0 && c.Version != 1 {
		return nil, fmt.Errorf("config: unsupported version %d", c.Version)
	}
	if len(c.Topics) == 0 {
		return nil, fmt.Errorf("config: no topics")
	}
	seen := map[string]bool{}
	for i := range c.Topics {
		t := &c.Topics[i]
		where := fmt.Sprintf("config: topics[%d]", i)
		if t.Topic == "" {
			return nil, fmt.Errorf("%s: topic is required", where)
		}
		if t.Schema == "" {
			return nil, fmt.Errorf("%s (%s): schema is required", where, t.Topic)
		}
		if t.Subject == "" {
			t.Subject = t.Topic + "-value" // Confluent's TopicNameStrategy
		}
		if seen[t.Subject] {
			return nil, fmt.Errorf("%s: duplicate subject %q", where, t.Subject)
		}
		seen[t.Subject] = true
		var err error
		if t.Format != "" {
			t.ResolvedFormat, err = compat.ParseFormat(t.Format)
		} else {
			t.ResolvedFormat, err = compat.FormatFromPath(t.Schema)
		}
		if err != nil {
			return nil, fmt.Errorf("%s (%s): %w", where, t.Topic, err)
		}
		if t.ResolvedMode, err = compat.ParseMode(firstNonEmpty(t.Mode, c.Defaults.Mode)); err != nil {
			return nil, fmt.Errorf("%s (%s): %w", where, t.Topic, err)
		}
		switch p := strings.ToLower(firstNonEmpty(t.JSONPolicy, c.Defaults.JSONPolicy, "strict")); p {
		case "strict", "lenient":
			t.ResolvedPolicy = jsonschema.Policy(p)
		default:
			return nil, fmt.Errorf("%s (%s): jsonPolicy must be strict or lenient", where, t.Topic)
		}
		switch e := strings.ToLower(firstNonEmpty(t.SampleEncoding, c.Defaults.SampleEncoding, "avro-json")); e {
		case "avro-json":
			t.ResolvedEncoding = avro.AvroJSON
		case "json", "plain-json":
			t.ResolvedEncoding = avro.PlainJSON
		default:
			return nil, fmt.Errorf("%s (%s): sampleEncoding must be avro-json or json", where, t.Topic)
		}
	}
	return &c, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// Path resolves a config-relative path.
func (c *Config) Path(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.Dir, p)
}

// HistoryFiles expands a topic's history glob, oldest first, using natural
// order so v2 sorts before v10.
func (c *Config) HistoryFiles(t Topic) ([]string, error) {
	if t.History == "" {
		return nil, nil
	}
	matches, err := filepath.Glob(c.Path(t.History))
	if err != nil {
		return nil, fmt.Errorf("history %q: %w", t.History, err)
	}
	sort.Slice(matches, func(i, j int) bool { return NaturalLess(matches[i], matches[j]) })
	return matches, nil
}

// NaturalLess compares strings treating digit runs as numbers.
func NaturalLess(a, b string) bool {
	for a != "" && b != "" {
		ra, rb := rune(a[0]), rune(b[0])
		if unicode.IsDigit(ra) && unicode.IsDigit(rb) {
			na, resta := digits(a)
			nb, restb := digits(b)
			if na != nb {
				return na < nb
			}
			a, b = resta, restb
			continue
		}
		if ra != rb {
			return ra < rb
		}
		a, b = a[1:], b[1:]
	}
	return len(a) < len(b)
}

func digits(s string) (uint64, string) {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	n, _ := strconv.ParseUint(s[:i], 10, 64)
	return n, s[i:]
}
