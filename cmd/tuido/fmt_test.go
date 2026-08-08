package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ramtinJ95/tuido/internal/task"
)

// fmtNow is a Saturday, so "monday" resolves to the 10th and "saturday" to the
// 15th (strictly future).
var fmtNow = time.Date(2026, 8, 8, 12, 0, 0, 0, time.Local)

func expand(t *testing.T, in string) (string, []string) {
	t.Helper()
	f, err := task.Parse("in.md", []byte(in))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	f, _, err = fixCheckboxes(f, fmtNow)
	if err != nil {
		t.Fatalf("fixCheckboxes: %v", err)
	}
	_, warnings := expandFile(f, fmtNow)
	return string(f.Bytes()), warnings
}

func TestExpandShorthand(t *testing.T) {
	cases := []struct {
		name, in, want string
		warnings       int
	}{
		{"prio and weekday due",
			"- [ ] rotate token :p2 :due monday\n",
			"- [ ] rotate token ⏫ 📅 2026-08-10 ➕ 2026-08-08\n", 0},
		{"p1 highest",
			"- [ ] x :p1\n",
			"- [ ] x 🔺 ➕ 2026-08-08\n", 0},
		{"p5 lowest",
			"- [ ] x :p5\n",
			"- [ ] x ⏬ ➕ 2026-08-08\n", 0},
		{"prio by name",
			"- [ ] x :prio high\n",
			"- [ ] x ⏫ ➕ 2026-08-08\n", 0},
		{"prio by digit",
			"- [ ] x :prio 4\n",
			"- [ ] x 🔽 ➕ 2026-08-08\n", 0},
		{"start sched due together",
			"- [ ] x :start today :sched tomorrow :due 2026-12-01\n",
			"- [ ] x 🛫 2026-08-08 ⏳ 2026-08-09 📅 2026-12-01 ➕ 2026-08-08\n", 0},
		{"existing created is preserved",
			"- [ ] x :p2 ➕ 2026-01-01\n",
			"- [ ] x ⏫ ➕ 2026-01-01\n", 0},
		{"duplicate due stays literal",
			"- [ ] x 📅 2026-01-01 :due monday\n",
			"- [ ] x 📅 2026-01-01 :due monday\n", 1},
		{"second priority stays literal",
			"- [ ] x :p2 :p3\n",
			"- [ ] x :p3 ⏫ ➕ 2026-08-08\n", 1},
		{"bad date stays literal",
			"- [ ] x :due nonsense\n",
			"- [ ] x :due nonsense\n", 1},
		{"missing value stays literal",
			"- [ ] x :due\n",
			"- [ ] x :due\n", 1},
		{"key as value stays literal, next token applies",
			"- [ ] x :due :p2\n",
			"- [ ] x :due ⏫ ➕ 2026-08-08\n", 1},
		{"unknown key warns and stays",
			"- [ ] see :help maybe\n",
			"- [ ] see :help maybe\n", 1},
		{"colons in words are not tokens",
			"- [ ] meet at 12:30 about foo:due\n",
			"- [ ] meet at 12:30 about foo:due\n", 0},
		{"fences are never touched",
			"```\n- [ ] x :p2\n```\n",
			"```\n- [ ] x :p2\n```\n", 0},
		{"continuation line with a field marker warns",
			"- [ ] parent :p2\n  wrapped 📅 2026-01-01\n",
			"- [ ] parent ⏫ ➕ 2026-08-08\n  wrapped 📅 2026-01-01\n", 1},
		{"continuation line with shorthand warns, parent untouched",
			"- [ ] parent\n  :due monday\n",
			"- [ ] parent\n  :due monday\n", 1},
		{"unrewritable task is skipped",
			"- [ ] x ⏫ ⏫ :due monday\n",
			"- [ ] x ⏫ ⏫ :due monday\n", 1},
		{"new stamps created and nothing else",
			"- [ ] test task :new\n",
			"- [ ] test task ➕ 2026-08-08\n", 0},
		{"new with created already set stays literal",
			"- [ ] x :new ➕ 2026-01-01\n",
			"- [ ] x :new ➕ 2026-01-01\n", 1},
		{"empty checkbox becomes a task and is stamped",
			"- [] test task\n",
			"- [ ] test task ➕ 2026-08-08\n", 0},
		{"empty checkbox with shorthand expands too",
			"- [] x :p2 :due monday\n",
			"- [ ] x ⏫ 📅 2026-08-10 ➕ 2026-08-08\n", 0},
		{"empty checkbox keeps an existing created date",
			"- [] x ➕ 2026-01-01\n",
			"- [ ] x ➕ 2026-01-01\n", 0},
		{"empty checkbox as an indented subtask is fixed",
			"- [ ] parent\n  - [] child\n",
			"- [ ] parent\n  - [ ] child ➕ 2026-08-08\n", 0},
		{"empty checkbox in a fence is untouched",
			"```\n- [] x\n```\n",
			"```\n- [] x\n```\n", 0},
		{"empty checkbox glued to text is not a checkbox",
			"- []byte is a slice\n",
			"- []byte is a slice\n", 0},
		{"done closes and stamps completion, not creation",
			"- [ ] rotate token ⏫ :done\n",
			"- [x] rotate token ⏫ ✅ 2026-08-08\n", 0},
		{"done on a hand-flipped checkbox just adds the date",
			"- [x] rotate token :done\n",
			"- [x] rotate token ✅ 2026-08-08\n", 0},
		{"done on a closed task stays literal",
			"- [x] x :done ✅ 2026-01-01\n",
			"- [x] x :done ✅ 2026-01-01\n", 1},
		{"drop cancels with a date",
			"- [ ] x :drop\n",
			"- [-] x ❌ 2026-08-08\n", 0},
		{"cancel is an alias for drop",
			"- [ ] x :cancel\n",
			"- [-] x ❌ 2026-08-08\n", 0},
		{"capture tokens still stamp created alongside done",
			"- [ ] x :p2 :done\n",
			"- [x] x ⏫ ➕ 2026-08-08 ✅ 2026-08-08\n", 0},
		{"bare dash bullet becomes a task and is stamped",
			"- rotate the token for x\n",
			"- [ ] rotate the token for x ➕ 2026-08-08\n", 0},
		{"bare dash bullet with shorthand expands too",
			"- fix thing :done\n",
			"- [x] fix thing ➕ 2026-08-08 ✅ 2026-08-08\n", 0},
		{"star and plus bullets are notes, never touched",
			"* a plain note\n+ another note\n",
			"* a plain note\n+ another note\n", 0},
		{"bracketed bullet is a link or checkbox attempt, not a bare task",
			"- [the design doc](https://example.com)\n",
			"- [the design doc](https://example.com)\n", 0},
		{"dash-only horizontal rule is not a task",
			"- - -\n",
			"- - -\n", 0},
		{"indented dash bullet under a task becomes a subtask",
			"- [ ] parent\n  - remember the edge case\n",
			"- [ ] parent\n  - [ ] remember the edge case ➕ 2026-08-08\n", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, warnings := expand(t, c.in)
			if got != c.want {
				t.Errorf("expand(%q):\n got %q\nwant %q", c.in, got, c.want)
			}
			if len(warnings) != c.warnings {
				t.Errorf("expand(%q): %d warnings, want %d: %v", c.in, len(warnings), c.warnings, warnings)
			}

			// fmt is a fixpoint: expanding its own output changes nothing.
			again, _ := expand(t, got)
			if again != got {
				t.Errorf("not idempotent:\nfirst  %q\nsecond %q", got, again)
			}
		})
	}
}

// runStdin is env.run with the given stdin, for the filter mode.
func (e *env) runStdin(stdin string, args ...string) result {
	e.t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(),
		"TUIDO_TEST_SUBPROCESS=1",
		"XDG_CONFIG_HOME="+filepath.Join(e.home, "config"),
		"XDG_STATE_HOME="+filepath.Join(e.home, "state"),
		"XDG_CACHE_HOME="+filepath.Join(e.home, "cache"),
		"NO_COLOR=1",
		"TUIDO_WORKSPACE=",
	)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		e.t.Fatalf("running %v: %v", args, err)
	}
	return result{out.String(), errb.String(), code}
}

func TestFmtRewritesListsInPlace(t *testing.T) {
	e := newEnv(t)
	e.init()
	e.write("work/inbox.md", "- [ ] rotate certs :p2 :due 2026-09-01\n- [ ] plain line\n")

	out := e.mustRun("fmt").stdout
	if !strings.Contains(out, "✓ fmt work/inbox (2 expanded)") {
		t.Errorf("unexpected fmt output:\n%s", out)
	}
	body := e.read("work/inbox.md")
	if !strings.Contains(body, "- [ ] rotate certs ⏫ 📅 2026-09-01 ➕ ") {
		t.Errorf("shorthand not expanded:\n%s", body)
	}
	if !strings.Contains(body, "- [ ] plain line\n") {
		t.Errorf("plain line disturbed:\n%s", body)
	}

	// The second run is a no-op, and says so.
	again := e.mustRun("fmt")
	if !strings.Contains(again.stdout, "· nothing to expand") {
		t.Errorf("second run not a no-op:\n%s", again.stdout)
	}
	if e.read("work/inbox.md") != body {
		t.Error("second fmt changed bytes")
	}
}

func TestFmtStdinFiltersAndKeepsWarningsOffStdout(t *testing.T) {
	e := newEnv(t)
	// No init: the filter must work without a store.
	r := e.runStdin("- [ ] x :due 2026-09-01\n- [ ] see :zzz\n", "fmt", "-")
	if r.code != 0 {
		t.Fatalf("fmt - exited %d: %s", r.code, r.stderr)
	}
	if !strings.Contains(r.stdout, "- [ ] x 📅 2026-09-01 ➕ 20") {
		t.Errorf("stdout not expanded:\n%s", r.stdout)
	}
	if !strings.Contains(r.stdout, "- [ ] see :zzz\n") {
		t.Errorf("unknown token not preserved:\n%s", r.stdout)
	}
	if strings.Contains(r.stdout, "⚠") {
		t.Errorf("warning leaked into stdout:\n%s", r.stdout)
	}
	if !strings.Contains(r.stderr, "unknown shorthand :zzz") {
		t.Errorf("warning missing from stderr:\n%s", r.stderr)
	}
}

func TestFmtStdinRefusesConflictMarkers(t *testing.T) {
	e := newEnv(t)
	r := e.runStdin("<<<<<<< HEAD\n- [ ] x :p2\n", "fmt", "-")
	if r.code != exitConflict {
		t.Errorf("conflicted stdin exited %d, want %d\nstderr: %s", r.code, exitConflict, r.stderr)
	}
	if r.stdout != "" {
		t.Errorf("conflicted input must produce no output, got:\n%s", r.stdout)
	}
}
