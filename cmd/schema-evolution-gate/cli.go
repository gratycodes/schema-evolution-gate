package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/gratycodes/schema-evolution-gate/internal/avro"
	"github.com/gratycodes/schema-evolution-gate/internal/compat"
	"github.com/gratycodes/schema-evolution-gate/internal/config"
	"github.com/gratycodes/schema-evolution-gate/internal/gate"
	"github.com/gratycodes/schema-evolution-gate/internal/jsonschema"
	"github.com/gratycodes/schema-evolution-gate/internal/replay"
	"github.com/gratycodes/schema-evolution-gate/internal/report"
)

const usage = `schema-evolution-gate: fail pull requests that break event schema compatibility.

Usage:
  schema-evolution-gate check   [--config .schema-gate.yaml] [--base origin/main] [--output table|json|markdown]
                                [--markdown-out FILE] [--json-out FILE] [--all] [--no-merge-base]
  schema-evolution-gate compare --old FILE [--old FILE ...] --new FILE [--mode BACKWARD] [--schema-format avro|json]
                                [--json-policy strict|lenient] [--output table|json|markdown]
  schema-evolution-gate replay  --schema FILE [--base FILE] --samples DIR [--schema-format avro|json]
                                [--encoding avro-json|json]
  schema-evolution-gate version

Exit codes: 0 compatible, 1 breaking changes or invalid schemas, 2 usage or setup error.
`

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// versionOverride pins the version in tests; buildVersion is set with
// -ldflags "-X main.buildVersion=v1.2.3".
var versionOverride, buildVersion string

func version() string {
	if versionOverride != "" {
		return versionOverride
	}
	if buildVersion != "" {
		return buildVersion
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

func run(args []string, stdout, stderr io.Writer) int {
	report.Version = version()
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	var err error
	var code int
	switch args[0] {
	case "check":
		code, err = cmdCheck(args[1:], stdout, stderr)
	case "compare":
		code, err = cmdCompare(args[1:], stdout, stderr)
	case "replay":
		code, err = cmdReplay(args[1:], stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, report.Version)
		return exitOK
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return exitOK
	default:
		err = fmt.Errorf("unknown command %q\n\n%s", args[0], usage)
	}
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stderr, "schema-evolution-gate:", err)
		}
		return exitUsage
	}
	return code
}

func newFlags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

func render(w io.Writer, format string, r gate.Report) error {
	switch format {
	case "table", "":
		return report.Table(w, r)
	case "json":
		return report.JSON(w, r)
	case "markdown", "md":
		return report.Markdown(w, r)
	}
	return fmt.Errorf("unknown output %q (want table, json or markdown)", format)
}

func writeFile(path string, fn func(io.Writer) error) error {
	var buf bytes.Buffer
	if err := fn(&buf); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), 0o644)
}

func exitFor(r gate.Report) int {
	if r.FailedTopics() > 0 {
		return exitBreaking
	}
	return exitOK
}

func cmdCheck(args []string, stdout, stderr io.Writer) (int, error) {
	fs := newFlags("check", stderr)
	cfgPath := fs.String("config", config.DefaultFile, "config file")
	base := fs.String("base", "", "git ref to compare against, e.g. origin/main (default: $GITHUB_BASE_REF as origin/<ref>, else history files only)")
	output := fs.String("output", "table", "stdout format: table, json or markdown")
	mdOut := fs.String("markdown-out", "", "also write the Markdown PR comment to this file")
	jsonOut := fs.String("json-out", "", "also write the JSON report to this file")
	all := fs.Bool("all", false, "check topics whose schema did not change too")
	noMB := fs.Bool("no-merge-base", false, "compare with the base ref itself, not its merge base with HEAD")
	if err := fs.Parse(args); err != nil {
		return exitUsage, err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return exitUsage, err
	}
	ref := *base
	if ref == "" && os.Getenv("GITHUB_BASE_REF") != "" {
		ref = "origin/" + os.Getenv("GITHUB_BASE_REF")
	}
	rep, err := gate.Run(cfg, gate.Options{BaseRef: ref, NoMergeBase: *noMB, All: *all})
	if err != nil {
		return exitUsage, err
	}
	if *mdOut != "" {
		if err := writeFile(*mdOut, func(w io.Writer) error { return report.Markdown(w, rep) }); err != nil {
			return exitUsage, err
		}
	}
	if *jsonOut != "" {
		if err := writeFile(*jsonOut, func(w io.Writer) error { return report.JSON(w, rep) }); err != nil {
			return exitUsage, err
		}
	}
	if err := render(stdout, *output, rep); err != nil {
		return exitUsage, err
	}
	return exitFor(rep), nil
}

func schemaFormat(flagValue, path string) (compat.Format, error) {
	if flagValue != "" {
		return compat.ParseFormat(flagValue)
	}
	return compat.FormatFromPath(path)
}

func cmdCompare(args []string, stdout, stderr io.Writer) (int, error) {
	fs := newFlags("compare", stderr)
	var olds multiFlag
	fs.Var(&olds, "old", "previous schema version; repeat oldest first for transitive modes")
	newPath := fs.String("new", "", "proposed schema")
	mode := fs.String("mode", "BACKWARD", "compatibility mode")
	sf := fs.String("schema-format", "", "avro or json (default: from file extension)")
	policy := fs.String("json-policy", "strict", "JSON Schema policy: strict or lenient")
	output := fs.String("output", "table", "table, json or markdown")
	if err := fs.Parse(args); err != nil {
		return exitUsage, err
	}
	if *newPath == "" || len(olds) == 0 {
		return exitUsage, errors.New("compare needs --new and at least one --old")
	}
	format, err := schemaFormat(*sf, *newPath)
	if err != nil {
		return exitUsage, err
	}
	m, err := compat.ParseMode(*mode)
	if err != nil {
		return exitUsage, err
	}
	if *policy != "strict" && *policy != "lenient" {
		return exitUsage, fmt.Errorf("--json-policy must be strict or lenient")
	}
	var history []compat.Version
	for _, p := range olds {
		data, err := os.ReadFile(p)
		if err != nil {
			return exitUsage, err
		}
		history = append(history, compat.Version{Label: p, Data: data})
	}
	head, err := os.ReadFile(*newPath)
	if err != nil {
		return exitUsage, err
	}
	tr := gate.TopicReport{Topic: filepath.Base(*newPath), Subject: "-", Schema: *newPath, Format: format, Mode: m}
	res, err := compat.Check(compat.Options{Format: format, Mode: m, JSONPolicy: jsonschema.Policy(*policy)},
		history, compat.Version{Label: *newPath, Data: head})
	if err != nil {
		tr.Status, tr.Error = gate.Error, err.Error()
	} else {
		tr.Result = &res
		tr.Status = gate.Pass
		if !res.Compatible {
			tr.Status = gate.Fail
		}
	}
	rep := gate.Report{Topics: []gate.TopicReport{tr}}
	if err := render(stdout, *output, rep); err != nil {
		return exitUsage, err
	}
	return exitFor(rep), nil
}

func cmdReplay(args []string, stdout, stderr io.Writer) (int, error) {
	fs := newFlags("replay", stderr)
	schemaPath := fs.String("schema", "", "proposed schema")
	basePath := fs.String("base", "", "schema the samples were produced with (optional)")
	samplesDir := fs.String("samples", "", "directory of *.json / *.jsonl payloads")
	sf := fs.String("schema-format", "", "avro or json (default: from file extension)")
	encoding := fs.String("encoding", "avro-json", "Avro sample encoding: avro-json or json")
	output := fs.String("output", "table", "table, json or markdown")
	if err := fs.Parse(args); err != nil {
		return exitUsage, err
	}
	if *schemaPath == "" || *samplesDir == "" {
		return exitUsage, errors.New("replay needs --schema and --samples")
	}
	format, err := schemaFormat(*sf, *schemaPath)
	if err != nil {
		return exitUsage, err
	}
	enc := avro.AvroJSON
	switch *encoding {
	case "avro-json":
	case "json":
		enc = avro.PlainJSON
	default:
		return exitUsage, fmt.Errorf("--encoding must be avro-json or json")
	}
	head, err := os.ReadFile(*schemaPath)
	if err != nil {
		return exitUsage, err
	}
	var base []byte
	if *basePath != "" {
		if base, err = os.ReadFile(*basePath); err != nil {
			return exitUsage, err
		}
	}
	samples, err := replay.LoadSamples(*samplesDir)
	if err != nil {
		return exitUsage, err
	}
	tr := gate.TopicReport{Topic: filepath.Base(*schemaPath), Subject: "-", Schema: *schemaPath, Format: format, Mode: "REPLAY", Status: gate.Pass}
	rr, err := replay.Run(format, base, head, samples, enc)
	if err != nil {
		tr.Status, tr.Error = gate.Error, err.Error()
	} else {
		tr.Replay = &rr
		if rr.Failed > 0 {
			tr.Status = gate.Fail
		}
	}
	rep := gate.Report{Topics: []gate.TopicReport{tr}}
	if err := render(stdout, *output, rep); err != nil {
		return exitUsage, err
	}
	return exitFor(rep), nil
}
