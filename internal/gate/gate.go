// Package gate runs every configured topic: it reads the base version from
// git, the proposed version from the work tree, applies the topic's
// compatibility mode against base plus history, and replays samples.
package gate

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gratycodes/schema-evolution-gate/internal/compat"
	"github.com/gratycodes/schema-evolution-gate/internal/config"
	"github.com/gratycodes/schema-evolution-gate/internal/gitsrc"
	"github.com/gratycodes/schema-evolution-gate/internal/replay"
)

// Status of one topic.
type Status string

// Topic statuses.
const (
	Pass      Status = "pass"
	Fail      Status = "fail"
	Unchanged Status = "unchanged"
	New       Status = "new"
	Error     Status = "error"
)

// TopicReport is the outcome for one topic.
type TopicReport struct {
	Topic   string         `json:"topic"`
	Subject string         `json:"subject"`
	Schema  string         `json:"schema"`
	Format  compat.Format  `json:"format"`
	Mode    compat.Mode    `json:"mode"`
	Status  Status         `json:"status"`
	Result  *compat.Result `json:"result,omitempty"`
	Replay  *replay.Result `json:"replay,omitempty"`
	Error   string         `json:"error,omitempty"`
}

// Failed reports whether this topic should fail the gate.
func (t TopicReport) Failed() bool { return t.Status == Fail || t.Status == Error }

// Report is the outcome for a whole run.
type Report struct {
	Base   string        `json:"base,omitempty"`
	Topics []TopicReport `json:"topics"`
}

// Breaking counts breaking changes across topics.
func (r Report) Breaking() int {
	n := 0
	for _, t := range r.Topics {
		if t.Result != nil {
			n += len(t.Result.Issues)
		}
	}
	return n
}

// FailedTopics counts topics that fail the gate.
func (r Report) FailedTopics() int {
	n := 0
	for _, t := range r.Topics {
		if t.Failed() {
			n++
		}
	}
	return n
}

// Options for Run.
type Options struct {
	// BaseRef is the git ref to compare against (e.g. origin/main). Empty
	// means no git: only history files are used.
	BaseRef string
	// NoMergeBase compares against BaseRef itself instead of its merge base
	// with HEAD.
	NoMergeBase bool
	// All checks topics whose schema file did not change too.
	All bool
}

// Run checks every topic in cfg.
func Run(cfg *config.Config, opts Options) (Report, error) {
	var rep Report
	var repo *gitsrc.Repo
	var baseCommit string
	if opts.BaseRef != "" {
		var err error
		if repo, err = gitsrc.Open(cfg.Dir); err != nil {
			return rep, err
		}
		if baseCommit, err = repo.Resolve(opts.BaseRef); err != nil {
			return rep, err
		}
		if !opts.NoMergeBase {
			if mb, err := repo.MergeBase(baseCommit, "HEAD"); err == nil {
				baseCommit = mb
			}
		}
		rep.Base = fmt.Sprintf("%s (%s)", opts.BaseRef, baseCommit[:12])
	}
	for _, t := range cfg.Topics {
		rep.Topics = append(rep.Topics, runTopic(cfg, t, repo, baseCommit, opts))
	}
	return rep, nil
}

func runTopic(cfg *config.Config, t config.Topic, repo *gitsrc.Repo, baseCommit string, opts Options) TopicReport {
	tr := TopicReport{Topic: t.Topic, Subject: t.Subject, Schema: t.Schema, Format: t.ResolvedFormat, Mode: t.ResolvedMode}
	fail := func(err error) TopicReport {
		tr.Status, tr.Error = Error, err.Error()
		return tr
	}
	headPath := cfg.Path(t.Schema)
	head, err := os.ReadFile(headPath)
	if err != nil {
		return fail(fmt.Errorf("reading proposed schema: %w", err))
	}
	if _, err := compat.Parse(t.ResolvedFormat, head); err != nil {
		return fail(fmt.Errorf("proposed schema %s is invalid: %w", t.Schema, err))
	}

	var history []compat.Version
	files, err := cfg.HistoryFiles(t)
	if err != nil {
		return fail(err)
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return fail(err)
		}
		history = append(history, compat.Version{Label: filepath.Base(f), Data: data})
	}

	var base []byte
	if repo != nil {
		base, err = repo.Show(baseCommit, headPath)
		switch {
		case errors.Is(err, gitsrc.ErrNotFound):
			base = nil
		case err != nil:
			return fail(err)
		}
	}
	if base != nil && (len(history) == 0 || !bytes.Equal(history[len(history)-1].Data, base)) {
		history = append(history, compat.Version{Label: "base", Data: base})
	}

	if base != nil && bytes.Equal(base, head) && !opts.All {
		tr.Status = Unchanged
		return tr
	}
	if len(history) == 0 {
		tr.Status = New
	} else {
		res, err := compat.Check(compat.Options{Format: t.ResolvedFormat, Mode: t.ResolvedMode, JSONPolicy: t.ResolvedPolicy},
			history, compat.Version{Label: t.Schema, Data: head})
		if err != nil {
			return fail(err)
		}
		tr.Result = &res
		tr.Status = Pass
		if !res.Compatible {
			tr.Status = Fail
		}
	}

	if t.Samples != "" {
		samples, err := replay.LoadSamples(cfg.Path(t.Samples))
		if err != nil {
			return fail(err)
		}
		writer := base
		if writer == nil && len(history) > 0 {
			writer = history[len(history)-1].Data
		}
		rr, err := replay.Run(t.ResolvedFormat, writer, head, samples, t.ResolvedEncoding)
		if err != nil {
			return fail(err)
		}
		tr.Replay = &rr
		if rr.Failed > 0 {
			tr.Status = Fail
		}
	}
	return tr
}
