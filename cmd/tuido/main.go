// Command tuido is gofmt for todo lists, plus quick capture.
//
// The markdown files are the sole source of truth. If tuido is deleted, nothing
// is lost.
package main

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/term"

	"github.com/ramtinJ95/tuido/internal/match"
	"github.com/ramtinJ95/tuido/internal/render"
	"github.com/ramtinJ95/tuido/internal/store"
	"github.com/ramtinJ95/tuido/internal/task"
	"github.com/ramtinJ95/tuido/internal/vcs"
)

// Exit codes are part of the interface: every command is scriptable.
const (
	exitOK       = 0
	exitUser     = 1 // no match, ambiguous with no TTY, capture refusal
	exitInternal = 2
	exitConflict = 3 // a file has unresolved conflict markers
	exitNotInit  = 4 // no config and no TTY to run `tuido init` in
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		args = []string{"ls"}
	}
	cmd, rest := args[0], args[1:]

	var err error
	switch cmd {
	case "init":
		err = cmdInit(rest)
	case "add", "a":
		err = cmdAdd(rest)
	case "done", "d":
		err = cmdDone(rest)
	case "sort":
		err = cmdSort(rest)
	case "fmt":
		err = cmdFmt(rest)
	case "ls", "list":
		err = cmdLs(rest)
	case "open", "o":
		err = cmdOpen(rest)
	case "path":
		err = cmdPath(rest)
	case "use":
		err = cmdUse(rest)
	case "sync":
		err = cmdSync(rest)
	case "id":
		err = cmdID(rest)
	case "upgrade":
		err = cmdUpgrade(rest)
	case "show": // undocumented: the fzf preview helper
		err = cmdShow(rest)
	case "_lists", "_workspaces": // undocumented: shell completion helpers
		err = cmdNames(cmd, rest)
	case "_commit": // undocumented: commit-and-push one file, for editor save hooks
		err = cmdCommit(rest)
	case "internal-sync": // undocumented: the detached background job
		err = cmdInternalSync(rest)
	case "internal-update-check": // undocumented: the detached version check
		err = cmdInternalUpdateCheck(rest)
	case "help":
		err = cmdHelp(rest)
	case "-h", "--help":
		usage(os.Stdout)
		return exitOK
	case "version", "--version", "-v":
		printVersion()
		return exitOK
	default:
		render.Errorf("unknown command %q", cmd)
		usage(os.Stderr)
		return exitUser
	}

	// Asking for help is not a failure. Exiting non-zero here would tell any
	// caller probing the CLI that the command is broken.
	if err == nil || errors.Is(err, errHelpRequested) {
		return exitOK
	}
	code := exitCode(err)
	if code == exitNotInit {
		// The one error where the fix is a command, not a correction.
		render.Errorf("not initialised — run `tuido init`")
		return code
	}
	var cancelled *match.ErrCancelled
	if errors.As(err, &cancelled) {
		return exitUser // the user dismissed the picker; nothing to report
	}
	render.Errorf("%v", err)
	return code
}

func exitCode(err error) int {
	var (
		conflict *task.ErrConflicted
		noMatch  *match.ErrNoMatch
		ambig    *match.ErrAmbiguous
		cancel   *match.ErrCancelled
		ambList  *store.ErrAmbiguousList
		needSec  *store.ErrNeedsSection
		user     *userError
	)
	switch {
	case errors.Is(err, store.ErrNotInitialised):
		return exitNotInit
	case errors.As(err, &conflict):
		return exitConflict
	case errors.As(err, &noMatch), errors.As(err, &ambig), errors.As(err, &cancel),
		errors.As(err, &ambList), errors.As(err, &needSec), errors.As(err, &user):
		return exitUser
	}
	return exitInternal
}

// userError marks a mistake in what the user asked for, as opposed to a bug.
type userError struct{ msg string }

func (e *userError) Error() string { return e.msg }

func uerr(format string, args ...any) error {
	return &userError{msg: fmt.Sprintf(format, args...)}
}

// app is the resolved environment for one invocation.
type app struct {
	st   *store.Store
	repo *vcs.Repo
	r    *render.Renderer
}

func openApp(root, ws string) (*app, error) {
	st, err := store.Open(store.Options{Root: root, Workspace: ws})
	if errors.Is(err, store.ErrNotInitialised) && term.IsTerminal(int(os.Stdin.Fd())) {
		// A terminal is enough context to ask; anything else exits 4 rather
		// than guessing where the user's todo files should live.
		fmt.Println("no config found — let's set up (or run `tuido init` non-interactively)")
		fmt.Println()
		if ierr := cmdInit(nil); ierr != nil {
			return nil, ierr
		}
		fmt.Println()
		st, err = store.Open(store.Options{Root: root, Workspace: ws})
	}
	if err != nil {
		return nil, err
	}
	repo := vcs.New(st.Root, store.CacheDir(), st.Cfg.Git.Enabled, st.Cfg.Git.AutoPush)
	// Both fire and forget: the render below never waits for either.
	repo.MaybeFetch(st.Cfg.Git.FetchInterval())
	maybeCheckUpdate(st.Cfg)
	return &app{st: st, repo: repo, r: render.New(os.Stdout)}, nil
}

// warn prints the sync warning and update reminder, if any, above a command's
// output. Both are read from cache; neither waits for the network.
func (a *app) warn() {
	if msg := a.repo.Warning(); msg != "" {
		a.r.Warn(msg)
	}
	if msg := updateNotice(a.st.Cfg); msg != "" {
		a.r.Warn(msg)
	}
}

// commit records a mutation locally and kicks off a detached push.
func (a *app) commit(path, message string) {
	if err := a.repo.Commit(path, message); err != nil {
		render.Errorf("%v", err) // visible, but not fatal: the file is already written
		return
	}
	a.repo.PushAsync()
}

// candidates collects every task in scope for fuzzy matching.
func (a *app) candidates(lists []store.List, openOnly bool) ([]match.Candidate, error) {
	var out []match.Candidate
	for _, l := range lists {
		f, err := a.st.Read(l)
		if err != nil {
			return nil, err
		}
		for _, t := range f.Tasks() {
			if openOnly && t.State.Closed() {
				continue
			}
			out = append(out, match.Candidate{Task: t, Ref: l.Ref()})
		}
	}
	return out, nil
}

func usage(w *os.File) {
	fmt.Fprint(w, `tuido — gofmt for todo lists, plus quick capture

  tuido init  [--root <path>] [--workspace <name>] [--git|--no-git] [--remote <url>]
  tuido add   [flags] <text…>      capture a task  (-p prio  -d due  -t tag  -l list)
  tuido done  <fuzzy…>             mark a task done
  tuido sort  [list] [--by …]      reorder tasks within their blocks
  tuido fmt   [list] | fmt -       expand :p2 / :due monday shorthand into fields
  tuido ls    [list] [--all]       show actionable tasks
  tuido open  [query] [--root]     open a list, or the whole repo, in $EDITOR
  tuido path  [query]              print the resolved file path
  tuido use   [workspace]          switch or show the current workspace
  tuido sync  [--status]           blocking fetch, rebase and push
  tuido id    <fuzzy…>             stamp a short id on a task
  tuido upgrade [--check]          install the latest release

Flags may appear before or after the text; -- ends flag parsing.
Exit codes: 0 ok, 1 user error, 2 internal, 3 file conflicted, 4 not initialised.

Run "tuido help <command>" or "tuido <command> --help" for usage and examples.
`)
}
