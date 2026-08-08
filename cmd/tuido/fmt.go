package main

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/ramtinJ95/tuido/internal/task"
)

// The shorthand dialect: a plain-ASCII way to type fields, canonicalised into
// emoji by `tuido fmt` and never written back. A token is `:key` at a word
// boundary in the task line's description; keys are case-insensitive.
//
//	:p1 … :p5          priority, highest → lowest
//	:prio <v>          priority by name or digit 1–5
//	:due    <when>     📅
//	:start  <when>     🛫
//	:sched  <when>     ⏳
//	:new               nothing but the ➕ created stamp
//
// <when> is one token, anything parseWhen accepts. A token that cannot apply —
// field already set, bad or missing value, unknown key — stays in the text
// verbatim and is reported, matching how parseFields treats malformed emoji
// fields. Because consumed tokens are removed, running fmt twice is a no-op.
//
// fmt also repairs `- []` into `- [ ]`. The empty checkbox is invisible to
// every task parser (including Obsidian), so a typo there silently demotes a
// task to prose; treating it as the lazy way to type a new task makes that
// state impossible and stamps the line as created today.

// cmdFmt expands shorthand. `tuido fmt -` filters stdin to stdout, for editor
// integration; otherwise it rewrites lists in place, like sort.
func cmdFmt(args []string) error {
	fs := newFlagSet("fmt")
	fl := registerScope(fs)
	if err := fs.Parse(args); err != nil {
		return flagErr(err)
	}
	if fs.NArg() == 1 && fs.Arg(0) == "-" {
		return fmtStdin()
	}

	a, err := openApp(*fl.root, *fl.ws)
	if err != nil {
		return err
	}
	lists, err := a.st.FindLists(strings.Join(fs.Args(), " "), *fl.all)
	if err != nil {
		return err
	}
	if len(lists) == 0 {
		return uerr("no list matches %q", strings.Join(fs.Args(), " "))
	}

	now := time.Now()
	touched := 0
	for _, l := range lists {
		f, err := a.st.Read(l)
		if err != nil {
			return err
		}
		f, fixed, err := fixCheckboxes(f, now)
		if err != nil {
			return err
		}
		applied, warnings := expandFile(f, now)
		for _, w := range warnings {
			fmt.Println(w)
		}
		if !f.Dirty() && fixed == 0 {
			continue
		}
		if err := a.st.Write(f); err != nil {
			return err
		}
		touched++
		if fixed > 0 {
			fmt.Printf("✓ fmt %s (%d expanded, %d checkboxes fixed)\n", l.Ref(), applied, fixed)
		} else {
			fmt.Printf("✓ fmt %s (%d expanded)\n", l.Ref(), applied)
		}
		a.commit(l.Path, fmt.Sprintf("fmt: %s", l.Ref()))
	}
	if touched == 0 {
		fmt.Println("· nothing to expand")
	}
	return nil
}

// fmtStdin is the filter mode. Stdout is the document, so warnings go to
// stderr. It needs no store and works outside any tuido root.
func fmtStdin() error {
	b, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	f, err := task.Parse("stdin", b)
	if err != nil {
		return err // ErrConflicted exits 3; the caller must not use the output
	}
	now := time.Now()
	f, _, err = fixCheckboxes(f, now)
	if err != nil {
		return err
	}
	_, warnings := expandFile(f, now)
	for _, w := range warnings {
		fmt.Fprintln(os.Stderr, w)
	}
	_, err = os.Stdout.Write(f.Bytes())
	return err
}

var emptyBoxRE = regexp.MustCompile(`^([ \t]*)([-*+]) \[\]( .*)?$`)

// fixCheckboxes turns `- []` lines into real `- [ ]` tasks and stamps them as
// created now — the empty box only ever means a task typed the lazy way, and
// leaving it would keep the line invisible to every command. Fixed lines force
// a re-parse, so the returned file replaces the argument. Fences are excluded
// by construction: this only looks at prose and continuation lines.
func fixCheckboxes(f *task.File, now time.Time) (*task.File, int, error) {
	var fixed []int
	for i := range f.Lines {
		ln := &f.Lines[i]
		if ln.Kind != task.LineOther && ln.Kind != task.LineTaskCont {
			continue
		}
		if m := emptyBoxRE.FindStringSubmatch(ln.Raw); m != nil {
			ln.Raw = m[1] + m[2] + " [ ]" + m[3]
			fixed = append(fixed, i)
		}
	}
	if len(fixed) == 0 {
		return f, 0, nil
	}
	nf, err := task.Parse(f.Path, f.Bytes())
	if err != nil {
		return nil, 0, err
	}
	d := task.DateFromTime(now)
	for _, i := range fixed {
		if t := nf.Lines[i].Task; t != nil && t.Created == nil {
			t.SetCreated(&d)
		}
	}
	return nf, len(fixed), nil
}

// expandFile expands every task carrying shorthand and flags field markers on
// continuation lines, where they silently count for nothing — almost always
// the debris of a hard-wrapped task line.
func expandFile(f *task.File, now time.Time) (applied int, warnings []string) {
	for i, ln := range f.Lines {
		switch {
		case ln.Task != nil && hasShorthand(ln.Task.Desc):
			if err := ln.Task.Rewritable(); err != nil {
				warnings = append(warnings, fmt.Sprintf("⚠ %v — skipped", err))
				continue
			}
			n, w := expandTask(ln.Task, now)
			applied += n
			warnings = append(warnings, w...)
		case ln.Kind == task.LineTaskCont && (task.HasFieldMarker(ln.Raw) || hasShorthand(ln.Raw)):
			warnings = append(warnings, fmt.Sprintf(
				"⚠ %s:%d: field on a continuation line counts for nothing — fields belong on the task line (wrapped by accident?)",
				f.Path, i+1))
		}
	}
	return applied, warnings
}

// expandTask applies the shorthand tokens in t's description. Tokens that
// apply are consumed; the rest of the description is preserved word for word.
// If anything applied and the task has no ➕ date, today is stamped: shorthand
// marks the line as freshly captured. Bare task lines are never stamped —
// that would backdate tasks that predate tuido.
func expandTask(t *task.Task, now time.Time) (applied int, warnings []string) {
	warn := func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf("⚠ %s:%d: ", t.Path, t.Line)+fmt.Sprintf(format, args...))
	}

	fields := strings.Fields(t.Desc)
	var out []string
	prioSet := t.Priority != task.Normal
	for i := 0; i < len(fields); i++ {
		tok := fields[i]
		if !isShorthandToken(tok) {
			out = append(out, tok)
			continue
		}
		key := strings.ToLower(tok[1:])

		setPrio := func(p task.Priority) bool {
			if prioSet {
				warn("%s: priority already set — left as text", tok)
				return false
			}
			t.SetPriority(p)
			prioSet = true
			applied++
			return true
		}

		if len(key) == 2 && key[0] == 'p' && key[1] >= '1' && key[1] <= '5' {
			p, _ := shorthandPriority(key[1:])
			if !setPrio(p) {
				out = append(out, tok)
			}
			continue
		}

		if key == "new" {
			if t.Created != nil {
				warn("%s: created already set — left as text", tok)
				out = append(out, tok)
				continue
			}
			applied++
			continue
		}

		switch key {
		case "prio", "due", "start", "sched":
		default:
			warn("unknown shorthand %s — left as text", tok)
			out = append(out, tok)
			continue
		}

		// The remaining keys all take exactly one value token.
		if i+1 >= len(fields) || strings.HasPrefix(fields[i+1], ":") {
			warn("%s needs a value — left as text", tok)
			out = append(out, tok)
			continue
		}
		i++
		val := fields[i]
		keep := func() { out = append(out, tok, val) }

		if key == "prio" {
			p, err := shorthandPriority(val)
			if err != nil {
				warn("%s %s: %v — left as text", tok, val, err)
				keep()
				continue
			}
			if !setPrio(p) {
				keep()
			}
			continue
		}

		var cur *task.Date
		var set func(*task.Date)
		switch key {
		case "due":
			cur, set = t.Due, t.SetDue
		case "start":
			cur, set = t.Start, t.SetStart
		case "sched":
			cur, set = t.Scheduled, t.SetScheduled
		}
		if cur != nil {
			warn("%s: %s already set — left as text", tok, key)
			keep()
			continue
		}
		d, err := parseWhenAt(val, now)
		if err != nil {
			warn("%s %s: %v — left as text", tok, val, err)
			keep()
			continue
		}
		set(&d)
		applied++
	}

	if applied == 0 {
		return 0, warnings
	}
	t.SetDesc(strings.Join(out, " "))
	if t.Created == nil {
		d := task.DateFromTime(now)
		t.SetCreated(&d)
	}
	return applied, warnings
}

// shorthandPriority resolves a :prio value: a digit 1–5 (highest → lowest) or
// any name ParsePriority knows.
func shorthandPriority(v string) (task.Priority, error) {
	switch v {
	case "1":
		return task.Highest, nil
	case "2":
		return task.High, nil
	case "3":
		return task.Medium, nil
	case "4":
		return task.Low, nil
	case "5":
		return task.Lowest, nil
	}
	return task.ParsePriority(v)
}

// hasShorthand reports whether any word looks like a shorthand token. It gates
// both expansion and the Rewritable check, so plain lines are never touched.
func hasShorthand(s string) bool {
	for _, tok := range strings.Fields(s) {
		if isShorthandToken(tok) {
			return true
		}
	}
	return false
}

// isShorthandToken: leading ':' then an alphanumeric — so `12:30`, `foo:due`
// and `:-)` are all plain text.
func isShorthandToken(tok string) bool {
	if len(tok) < 2 || tok[0] != ':' {
		return false
	}
	c := tok[1]
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}
