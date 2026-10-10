package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gratycodes/schema-evolution-gate/internal/compat"
	"github.com/gratycodes/schema-evolution-gate/internal/gate"
)

func TestSummaryAndHeadline(t *testing.T) {
	tests := []struct {
		name string
		r    gate.Report
		want []string // substrings of the markdown output
		not  []string
	}{
		{
			name: "all pass",
			r: gate.Report{Topics: []gate.TopicReport{
				{Topic: "a", Subject: "a-value", Format: compat.Avro, Mode: compat.Backward, Status: gate.Pass, Result: &compat.Result{Compatible: true}},
				{Topic: "b", Subject: "b-value", Format: compat.JSONSchema, Mode: compat.Backward, Status: gate.Unchanged},
			}},
			want: []string{Marker, "✅ Schema evolution gate passed: 1 changed topic checked", "| `a` | `a-value` | Avro | `BACKWARD` | ✅ compatible |", "➖ unchanged"},
			not:  []string{"####"},
		},
		{
			name: "new subject and error",
			r: gate.Report{Topics: []gate.TopicReport{
				{Topic: "n", Subject: "n-value", Format: compat.Avro, Mode: compat.Full, Status: gate.New},
				{Topic: "e", Subject: "e-value", Format: compat.JSONSchema, Mode: compat.Full, Status: gate.Error, Error: "parse x | y:\nbad"},
			}},
			want: []string{"❌ Schema evolution gate failed: 0 breaking changes, 1 of 2 topics failing", "new subject", `error: parse x \| y: bad`},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var md, table, js bytes.Buffer
			if err := Markdown(&md, tc.r); err != nil {
				t.Fatal(err)
			}
			if err := Table(&table, tc.r); err != nil {
				t.Fatal(err)
			}
			if err := JSON(&js, tc.r); err != nil {
				t.Fatal(err)
			}
			for _, w := range tc.want {
				if !strings.Contains(md.String(), w) {
					t.Errorf("markdown missing %q:\n%s", w, md.String())
				}
			}
			for _, w := range tc.not {
				if strings.Contains(md.String(), w) {
					t.Errorf("markdown unexpectedly contains %q", w)
				}
			}
			if strings.Contains(md.String(), "\n\n\n") {
				t.Errorf("markdown has a double blank line:\n%s", md.String())
			}
			if !strings.Contains(js.String(), `"passed"`) || table.Len() == 0 {
				t.Errorf("json/table output incomplete")
			}
		})
	}
}

func TestCodeAndCellEscaping(t *testing.T) {
	if got := code("a`b"); got != "`a'b`" {
		t.Errorf("code = %q", got)
	}
	if got := cell("a|b\nc"); got != `a\|b c` {
		t.Errorf("cell = %q", got)
	}
}
