package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/pflag"
)

// errHelpRequested is returned when -h/--help was parsed. It is not a failure:
// asking for help exits 0, so a caller probing the CLI is not told the command
// broke.
var errHelpRequested = errors.New("help requested")

// help is the per-command documentation. Positional arguments do not appear in
// a flag dump, so the synopsis carries them, and every command gets worked
// examples — that is the difference between a CLI something can discover and
// one it has to guess at.
type help struct {
	summary  string
	synopsis []string
	notes    []string
	examples []string
}

var helps = map[string]help{
	"init": {
		summary: "first-run setup",
		synopsis: []string{
			"tuido init [flags]",
		},
		notes: []string{
			"Prompts interactively when stdin is a terminal. Every prompt has a flag",
			"equivalent, so a dotfiles bootstrap can run it non-interactively.",
			"Re-running against a valid config changes nothing unless --force.",
			"If --remote points at a repo that already has commits, it is cloned",
			"rather than initialised, which is how a second machine adopts your tasks.",
		},
		examples: []string{
			"tuido init",
			"tuido init --root ~/notes/todo --workspace work",
			"tuido init --root ~/notes/todo --remote git@github.com:you/todo.git",
		},
	},
	"add": {
		summary: "capture a task",
		synopsis: []string{
			"tuido add [flags] <text…>    capture one task",
			"tuido add -                  read one task per line from stdin",
			"tuido add                    open $EDITOR on a scratch buffer",
		},
		notes: []string{
			"Stamps a creation date always. With no -l, the task goes to the current",
			"workspace's capture list. Capturing into a file that has headings requires",
			"-l <list>/<section>, or a `capture=` key in the file's marker comment.",
			"",
			"A bare weekday means the next strictly future occurrence: on a Friday,",
			"-d friday is next Friday, not today. Use -d today for today.",
		},
		examples: []string{
			"tuido add rotate vault certs -p high -d friday",
			"tuido add -p highest audit IAM policies -t security",
			"tuido add -l oncall/now page the on-call engineer",
			`printf 'buy milk\ncall plumber\n' | tuido add -`,
		},
	},
	"done": {
		summary: "mark a task done",
		synopsis: []string{
			"tuido done <fuzzy text…>",
		},
		notes: []string{
			"Every token must appear in the task's description. One match acts, zero",
			"errors, several open an fzf picker — or, with no terminal, print the",
			"candidates and exit 1 without changing anything.",
			"",
			"Sets the state and stamps a completion date. The line is not moved;",
			"finished work sinks on the next `tuido sort`.",
		},
		examples: []string{
			"tuido done vault certs",
			"tuido done drain --all",
		},
	},
	"sort": {
		summary: "reorder tasks within their blocks",
		synopsis: []string{
			"tuido sort [list] [flags]",
		},
		notes: []string{
			"Sorts task lines within each contiguous run of them. Headings, prose,",
			"blank lines and code fences are boundaries and never move; nested",
			"subtasks travel with their parent. Running it twice changes nothing.",
			"",
			"Order: open before closed, unblocked before blocked, then the --by key.",
			"Files whose marker says sort=none are skipped unless --by is explicit.",
		},
		examples: []string{
			"tuido sort",
			"tuido sort oncall --by due",
			"tuido sort --all",
		},
	},
	"fmt": {
		summary: "expand shorthand into canonical fields",
		synopsis: []string{
			"tuido fmt [list] [flags]    rewrite lists in place",
			"tuido fmt -                 filter stdin to stdout, for editor integration",
		},
		notes: []string{
			"Expands the plain-ASCII shorthand into the emoji dialect, so fields can",
			"be typed without leaving the keyboard:",
			"",
			"  :p1 … :p5        priority, highest → lowest",
			"  :prio <v>        priority by name or digit 1–5",
			"  :due <when>      due       (YYYY-MM-DD | today | tomorrow | week | <weekday>)",
			"  :start <when>    start     (same values)",
			"  :sched <when>    scheduled (same values)",
			"  :new             nothing but the creation stamp",
			"",
			"A task that had shorthand applied is stamped with a creation date if it",
			"lacks one; bare lines are never stamped. A token that cannot apply —",
			"field already set, bad value, unknown key — stays in the text and is",
			"reported. Running fmt twice changes nothing.",
			"",
			"An empty checkbox `- []` is the lazy way to type a new task: fmt repairs",
			"it to `- [ ]` and stamps it, since no parser would ever see it otherwise.",
		},
		examples: []string{
			"tuido fmt",
			"tuido fmt oncall",
			"printf -- '- [ ] rotate certs :p2 :due friday\\n' | tuido fmt -",
			"printf -- '- [] quick capture :new\\n' | tuido fmt -",
		},
	},
	"ls": {
		summary: "show actionable tasks",
		synopsis: []string{
			"tuido ls [list] [flags]",
		},
		notes: []string{
			"Hides completed, cancelled, blocked, not-yet-started and future-scheduled",
			"tasks, and prints a footer counting each reason. --all shows everything",
			"in every workspace.",
			"",
			"Colour is used only when stdout is a terminal, and comes from the",
			"terminal's own palette. NO_COLOR and TERM=dumb disable it.",
		},
		examples: []string{
			"tuido ls",
			"tuido ls oncall",
			"tuido ls -t infra --due week",
			"tuido ls --all",
		},
	},
	"open": {
		summary: "open a list, or the whole repo, in $EDITOR",
		synopsis: []string{
			"tuido open [query] [flags]",
		},
		notes: []string{
			"No query opens a picker over every list. The editor is started with its",
			"working directory at the repo root, so project-wide search is scoped to",
			"all your tasks.",
			"",
			"Note that --root here is the boolean below, not a path. To point this",
			"command at a different repo, set TUIDO_ROOT.",
		},
		examples: []string{
			"tuido open",
			"tuido open oncall",
			"tuido open --root",
		},
	},
	"path": {
		summary: "print the resolved file path",
		synopsis: []string{
			"tuido path [query] [flags]",
		},
		notes: []string{
			"The primitive `open` is built on. Prints one absolute path and exits.",
		},
		examples: []string{
			"tuido path oncall",
			"bat $(tuido path inbox)",
		},
	},
	"use": {
		summary: "switch or show the current workspace",
		synopsis: []string{
			"tuido use <workspace>    switch to it",
			"tuido use                print the current one and where it came from",
		},
		notes: []string{
			"Precedence: -w flag, then TUIDO_WORKSPACE, then this persisted context,",
			"then the config default.",
		},
		examples: []string{
			"tuido use personal",
			"tuido use",
		},
	},
	"sync": {
		summary: "blocking fetch, rebase and push",
		synopsis: []string{
			"tuido sync [flags]",
		},
		notes: []string{
			"Normal operation needs no sync: commits are automatic and pushing happens",
			"in the background. Use this to see what went wrong, or to resolve a",
			"divergence. On a content conflict it aborts cleanly and tells you where",
			"to look — it never resolves one itself.",
		},
		examples: []string{
			"tuido sync",
			"tuido sync --status",
		},
	},
	"upgrade": {
		summary: "install the latest release",
		synopsis: []string{
			"tuido upgrade [flags]",
		},
		notes: []string{
			"Downloads the release binary for this platform, verifies it against the",
			"published sha256 checksum, and replaces the running binary atomically.",
			"An unverifiable download is refused rather than installed.",
			"",
			"tuido checks for a new version at most once a day, in a detached",
			"background process, and mentions it in one line the next time you run a",
			"command. Nothing is ever downloaded or replaced without this command.",
			"Set check = false under [update] in the config to turn the reminder off.",
		},
		examples: []string{
			"tuido upgrade --check",
			"tuido upgrade",
		},
	},
	"id": {
		summary: "stamp a short id on a task",
		synopsis: []string{
			"tuido id <fuzzy text…>",
		},
		notes: []string{
			"Prints the task's id, assigning one first if it has none. Ids are never",
			"written automatically; this is the opt-in escape hatch for expressing a",
			"dependency with the blocked-by field.",
		},
		examples: []string{
			"tuido id migrate the bastion",
		},
	},
}

// newFlagSet builds a flag set whose -h/--help prints real documentation
// instead of a bare flag dump.
func newFlagSet(name string) *pflag.FlagSet {
	fs := pflag.NewFlagSet(name, pflag.ContinueOnError)
	fs.SetInterspersed(true) // flags may follow the text, which is how people type
	fs.SetOutput(io.Discard) // pflag's own messages are replaced by ours
	fs.Usage = func() { printCommandHelp(os.Stdout, name, fs) }
	return fs
}

// flagErr turns a parse failure into the right kind of error: asking for help
// is a success, and a bad flag is the user's mistake, not an internal fault.
func flagErr(err error) error {
	if errors.Is(err, pflag.ErrHelp) {
		return errHelpRequested
	}
	return uerr("%v", err)
}

func printCommandHelp(w io.Writer, name string, fs *pflag.FlagSet) {
	h, ok := helps[name]
	if !ok {
		fmt.Fprintf(w, "tuido %s\n", name)
		return
	}

	fmt.Fprintf(w, "tuido %s — %s\n\n", name, h.summary)
	fmt.Fprintln(w, "Usage:")
	for _, line := range h.synopsis {
		fmt.Fprintf(w, "  %s\n", line)
	}
	if defaults := strings.TrimRight(fs.FlagUsages(), "\n"); defaults != "" {
		fmt.Fprintf(w, "\nFlags:\n%s\n", defaults)
	}
	if len(h.notes) > 0 {
		fmt.Fprintln(w)
		for _, line := range h.notes {
			if line == "" {
				fmt.Fprintln(w)
				continue
			}
			fmt.Fprintf(w, "%s\n", line)
		}
	}
	if len(h.examples) > 0 {
		fmt.Fprintln(w, "\nExamples:")
		for _, e := range h.examples {
			fmt.Fprintf(w, "  %s\n", e)
		}
	}
	fmt.Fprintln(w, "\nExit codes: 0 ok, 1 user error, 2 internal, 3 file conflicted, 4 not initialised.")
}

// cmdHelp implements `tuido help [command]`, which is what people and agents
// reach for before they find --help.
func cmdHelp(args []string) error {
	if len(args) == 0 {
		usage(os.Stdout)
		return nil
	}
	name := args[0]
	if _, ok := helps[name]; !ok {
		return uerr("unknown command %q — run `tuido help` for the list", name)
	}
	// Rebuild the command's flag set so the help shows its real flags.
	fs := newFlagSet(name)
	registerFlags(name, fs)
	printCommandHelp(os.Stdout, name, fs)
	return nil
}

// commandNames is the documented command list, in a stable order.
func commandNames() []string {
	names := make([]string, 0, len(helps))
	for n := range helps {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
