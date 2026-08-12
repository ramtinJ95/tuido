package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ramtinJ95/tuido/internal/store"
)

// cmdAgents prints a briefing that teaches a coding agent how to work with
// the task files: the CLI, direct file edits, and the rules that keep both
// safe. It is written to be piped into an AGENTS.md or CLAUDE.md.
func cmdAgents(args []string) error {
	fs := newFlagSet("agents")
	fl := registerCommon(fs)
	if err := fs.Parse(args); err != nil {
		return flagErr(err)
	}

	// The briefing is more useful with this machine's actual paths in it,
	// but it is documentation: it must neither fail nor prompt for setup
	// when tuido is not initialised yet.
	var local string
	st, err := store.Open(store.Options{Root: *fl.root, Workspace: *fl.ws})
	switch {
	case err == nil:
		ws, _ := st.Workspace()
		local = fmt.Sprintf("On this machine the root is `%s`; workspaces: %s (current: %s).",
			st.Root, strings.Join(st.Workspaces(), ", "), ws)
	case errors.Is(err, store.ErrNotInitialised):
		local = "tuido is not initialised on this machine — run `tuido init` first."
	default:
		return err
	}

	fmt.Printf(briefing, local)
	return nil
}

const briefing = `# Working with tuido

tuido manages the todo lists here. Tasks live in plain markdown files, which
are the sole source of truth — there is no database or index behind them.
%s

There are two safe ways to change tasks. Prefer the CLI for single
operations, and direct file edits for bulk changes.

## The CLI

    tuido ls [list] [--all] [--json]   list tasks; --json emits a JSON array
    tuido add <text…> [-p prio] [-d due] [-t tag] [-l list]
    tuido done <words…>                mark done; every word must match the task
    tuido id <words…>                  print a task's stable id, assigning one if needed
    tuido path <list>                  print a list's absolute file path
    tuido fmt [list]                   expand :p1 / :due / :done shorthand (idempotent)
    tuido sort [list]                  reorder: open before done, then priority and due (idempotent)
    tuido archive [list] [--dry-run]   move closed tasks to the list's _archive mirror
    tuido ls --archived                read the archive back

Fuzzy commands fail safe: with no terminal, an ambiguous match prints the
candidates and exits 1 without changing anything. Recover by adding more
words, or by matching on an id from ` + "`tuido id`" + `.

Exit codes: 0 ok, 1 user error (no or ambiguous match, bad flag), 2 internal,
3 file conflicted, 4 not initialised. Errors go to stderr.

## Editing the files directly

Fine, and best for bulk edits — rewording, adding many tasks, moving tasks
between sections. One task per line, in the Obsidian Tasks emoji dialect:

    - [ ] rotate vault certs ⏫ 📅 2026-08-14 #infra

States: [ ] open, [/] in progress, [x] done, [-] cancelled. Instead of the
emoji fields you can type ASCII shorthand — :p1…:p5, :due friday,
:start monday, :done, :drop — then run ` + "`tuido fmt <list>`" + ` to expand it and
` + "`tuido sort <list>`" + ` to restore order. Both are idempotent, and sort never
moves a task across a heading.

## Rules

- Complete or cancel through ` + "`tuido done`" + `, :done or :drop — they stamp the
  completion date. Do not just flip a checkbox to [x] by hand.
- After editing a file directly, run ` + "`tuido fmt`" + ` then ` + "`tuido sort`" + ` on it.
- Do not run git in the todo root: tuido commits and pushes automatically.
- ` + "`tuido ls`" + ` hides done, cancelled, blocked and not-yet-started tasks;
  add --all to see everything. In --json output each task's ` + "`hidden`" + ` field
  says why a default ls would hide it.
- Never archive by hand: ` + "`tuido archive`" + ` moves closed tasks into
  _archive/ preserving their headings, and skips done tasks that still have
  open subtasks.
`
