// Package report renders gate results as a terminal table, JSON, or a
// Markdown pull request comment.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/gratycodes/schema-evolution-gate/internal/compat"
	"github.com/gratycodes/schema-evolution-gate/internal/gate"
	"github.com/gratycodes/schema-evolution-gate/internal/replay"
)

// Marker identifies the gate's PR comment so the Action can update it in place.
const Marker = "<!-- schema-evolution-gate -->"

// Version is stamped into reports; set at build time.
var Version = "dev"

// JSON writes the report as indented JSON.
func JSON(w io.Writer, r gate.Report) error {
	out := struct {
		gate.Report
		Breaking     int  `json:"breaking"`
		FailedTopics int  `json:"failedTopics"`
		Passed       bool `json:"passed"`
	}{r, r.Breaking(), r.FailedTopics(), r.FailedTopics() == 0}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func formatName(f compat.Format) string {
	if f == compat.JSONSchema {
		return "JSON Schema"
	}
	return "Avro"
}

func summary(t gate.TopicReport) string {
	var parts []string
	switch t.Status {
	case gate.Unchanged:
		return "unchanged"
	case gate.Error:
		return "error: " + t.Error
	case gate.New:
		parts = append(parts, "new subject")
	case gate.Pass:
		s := "compatible"
		if t.Result != nil && t.Result.Changes > 0 {
			s += fmt.Sprintf(" (%d non-breaking change%s)", t.Result.Changes, plural(t.Result.Changes))
		}
		parts = append(parts, s)
	case gate.Fail:
		if t.Result != nil && len(t.Result.Issues) > 0 {
			parts = append(parts, fmt.Sprintf("%d breaking", len(t.Result.Issues)))
		} else if t.Result != nil {
			parts = append(parts, "compatible by rules")
		}
	}
	if t.Replay != nil {
		parts = append(parts, fmt.Sprintf("replay %d/%d failed", t.Replay.Failed, t.Replay.Total))
	}
	return strings.Join(parts, ", ")
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func headline(r gate.Report) string {
	failed, breaking := r.FailedTopics(), r.Breaking()
	checked := 0
	for _, t := range r.Topics {
		if t.Status != gate.Unchanged {
			checked++
		}
	}
	if failed == 0 {
		return fmt.Sprintf("passed: %d changed topic%s checked, no breaking changes", checked, plural(checked))
	}
	return fmt.Sprintf("failed: %d breaking change%s, %d of %d topic%s failing", breaking, plural(breaking), failed, len(r.Topics), plural(len(r.Topics)))
}

// Table writes a plain-text report for terminals and CI logs.
func Table(w io.Writer, r gate.Report) error {
	fmt.Fprintf(w, "schema-evolution-gate %s\n", headline(r))
	if r.Base != "" {
		fmt.Fprintf(w, "base: %s\n", r.Base)
	}
	fmt.Fprintln(w)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TOPIC\tSUBJECT\tFORMAT\tMODE\tRESULT")
	for _, t := range r.Topics {
		result := strings.ToUpper(string(t.Status))
		if t.Status != gate.Unchanged {
			result += ": " + summary(t)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", t.Topic, t.Subject, formatName(t.Format), t.Mode, result)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	for _, t := range r.Topics {
		if t.Result != nil && len(t.Result.Issues) > 0 {
			fmt.Fprintf(w, "\n%s: %d breaking change%s\n", t.Subject, len(t.Result.Issues), plural(len(t.Result.Issues)))
			tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "  RULE\tFIELD\tDIRECTION\tAGAINST\tWHY")
			for _, is := range t.Result.Issues {
				fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\n", is.Rule, is.Field, is.Direction, is.Against, is.Reason)
			}
			if err := tw.Flush(); err != nil {
				return err
			}
		}
		if t.Replay != nil && (t.Replay.Failed > 0 || t.Replay.Stale > 0) {
			fmt.Fprintf(w, "\n%s replay: %d of %d recorded payloads fail against the new schema", t.Subject, t.Replay.Failed, t.Replay.Total)
			if t.Replay.Stale > 0 {
				fmt.Fprintf(w, " (%d stale: already invalid under the base schema)", t.Replay.Stale)
			}
			fmt.Fprintln(w)
			for _, f := range t.Replay.Failures {
				fmt.Fprintf(w, "  %s %s: %s\n", f.Sample, f.Path, f.Message)
			}
		}
	}
	return nil
}

func cell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.ReplaceAll(s, "\n", " ")
}

func code(s string) string {
	if s == "" {
		return ""
	}
	return "`" + strings.ReplaceAll(s, "`", "'") + "`"
}

// Markdown writes the pull request comment.
func Markdown(w io.Writer, r gate.Report) error {
	var b strings.Builder
	b.WriteString(Marker + "\n")
	icon := "✅"
	if r.FailedTopics() > 0 {
		icon = "❌"
	}
	fmt.Fprintf(&b, "### %s Schema evolution gate %s\n\n", icon, headline(r))
	if r.Base != "" {
		fmt.Fprintf(&b, "Compared with %s.\n\n", code(r.Base))
	}
	b.WriteString("| Topic | Subject | Format | Mode | Result |\n|---|---|---|---|---|\n")
	for _, t := range r.Topics {
		mark := map[gate.Status]string{gate.Pass: "✅", gate.Fail: "❌", gate.Error: "❌", gate.New: "🆕", gate.Unchanged: "➖"}[t.Status]
		if t.Status == gate.New && t.Failed() {
			mark = "❌"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s %s |\n", code(t.Topic), code(t.Subject), formatName(t.Format), code(string(t.Mode)), mark, cell(summary(t)))
	}
	for _, t := range r.Topics {
		hasIssues := t.Result != nil && len(t.Result.Issues) > 0
		hasReplay := t.Replay != nil && (t.Replay.Failed > 0 || t.Replay.Stale > 0)
		if !hasIssues && !hasReplay {
			continue
		}
		fmt.Fprintf(&b, "\n#### %s (%s)\n\n", code(t.Subject), code(t.Schema))
		if hasIssues {
			b.WriteString("| Rule | Field | Direction | Against | Why |\n|---|---|---|---|---|\n")
			for _, is := range t.Result.Issues {
				fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n", code(is.Rule), code(is.Field), is.Direction, code(is.Against), cell(is.Reason))
			}
		}
		if hasReplay {
			if hasIssues {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "**Replay:** %d of %d recorded payloads fail against the new schema.", t.Replay.Failed, t.Replay.Total)
			if t.Replay.Stale > 0 {
				fmt.Fprintf(&b, " %d sample%s already fail the base schema and were skipped.", t.Replay.Stale, plural(t.Replay.Stale))
			}
			b.WriteString("\n")
			if len(t.Replay.Failures) > 0 {
				b.WriteString("\n| Sample | Path | Error |\n|---|---|---|\n")
				for _, f := range t.Replay.Failures {
					fmt.Fprintf(&b, "| %s | %s | %s |\n", code(f.Sample), code(f.Path), cell(f.Message))
				}
				if len(t.Replay.Failures) == replay.MaxListed {
					fmt.Fprintf(&b, "\n_Only the first %d problems are listed._\n", replay.MaxListed)
				}
			}
		}
	}
	b.WriteString("\n<details><summary>How to read this</summary>\n\n")
	b.WriteString("- **backward**: consumers on the new schema reading events written with an older version. Breaks here mean old events on the topic (retention, replays, rewinds) become unreadable.\n")
	b.WriteString("- **forward**: consumers still on an older schema reading events from the new one. Breaks here mean producers cannot ship before every consumer upgrades.\n")
	b.WriteString("- Rules follow Confluent Schema Registry compatibility semantics (Avro resolution rules; JSON Schema diff types and policy). Replay validates recorded payloads from the repo against the proposed schema.\n")
	b.WriteString("</details>\n\n")
	fmt.Fprintf(&b, "<sub>schema-evolution-gate %s</sub>\n", Version)
	_, err := io.WriteString(w, b.String())
	return err
}
