package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/pflag"

	"github.com/ramtinJ95/tuido/internal/match"
	"github.com/ramtinJ95/tuido/internal/render"
	"github.com/ramtinJ95/tuido/internal/store"
	"github.com/ramtinJ95/tuido/internal/task"
)

// cmdLs shows actionable tasks: completed, cancelled, blocked, not-yet-started
// and future-scheduled work is hidden, with a footer counting each reason.
func cmdLs(args []string) error {
	fs := newFlagSet("ls")
	fl := registerLs(fs)
	if err := fs.Parse(args); err != nil {
		return flagErr(err)
	}
	all, tags, dueBy := fl.all, fl.tags, fl.dueBy

	a, err := openApp(*fl.root, *fl.ws)
	if err != nil {
		return err
	}
	lists, err := a.st.FindLists(strings.Join(fs.Args(), " "), *all)
	if err != nil {
		return err
	}
	if len(lists) == 0 {
		return uerr("no list matches %q", strings.Join(fs.Args(), " "))
	}

	var cutoff *task.Date
	if *dueBy != "" {
		d, err := parseWhen(*dueBy)
		if err != nil {
			return err
		}
		cutoff = &d
	}

	openIDs := a.st.OpenIDs(lists)
	today := task.Today()

	var groups []render.Group
	for _, l := range lists {
		f, err := a.st.Read(l)
		if err != nil {
			return err
		}
		g := render.Group{
			Ref:      l.Ref(),
			Headings: f.HeadingPaths(),
			Hidden:   map[string]int{},
		}
		for _, t := range f.Tasks() {
			if !matchesFilters(t, *tags, cutoff) {
				continue
			}
			if reason := t.Hidden(today, openIDs); reason != "" && !*all {
				g.Hidden[reason]++
				continue
			}
			g.Tasks = append(g.Tasks, t)
		}
		groups = append(groups, g)
	}

	if *fl.json {
		// Stdout must stay pure JSON, so the warnings move to stderr.
		a.r = render.New(os.Stderr)
		a.warn()
		return writeTasksJSON(os.Stdout, lists, groups, today, openIDs)
	}

	a.warn()
	a.r.Groups(groups, !*all)
	return nil
}

// jsonTask is the machine-readable view of one task, for `ls --json`. The
// field names are part of the interface: scripts and agents depend on them.
type jsonTask struct {
	Workspace   string   `json:"workspace"`
	List        string   `json:"list"`
	Path        string   `json:"path"`
	Line        int      `json:"line"`
	State       string   `json:"state"`
	Desc        string   `json:"desc"`
	Priority    string   `json:"priority"`
	Tags        []string `json:"tags,omitempty"`
	Due         string   `json:"due,omitempty"`
	Start       string   `json:"start,omitempty"`
	Scheduled   string   `json:"scheduled,omitempty"`
	Created     string   `json:"created,omitempty"`
	Completed   string   `json:"completed,omitempty"`
	CancelledOn string   `json:"cancelled_on,omitempty"`
	ID          string   `json:"id,omitempty"`
	BlockedBy   []string `json:"blocked_by,omitempty"`
	Recurrence  string   `json:"recurrence,omitempty"`
	Hidden      string   `json:"hidden,omitempty"`
}

// writeTasksJSON flattens the groups into one array. groups was built from
// lists in order, one group per list, which is what lets the two line up here.
func writeTasksJSON(w io.Writer, lists []store.List, groups []render.Group, today task.Date, openIDs map[string]bool) error {
	out := make([]jsonTask, 0) // an empty result is [], never null
	for i, g := range groups {
		l := lists[i]
		for _, t := range g.Tasks {
			out = append(out, jsonTask{
				Workspace:   l.Workspace,
				List:        l.Name,
				Path:        t.Path,
				Line:        t.Line,
				State:       t.State.String(),
				Desc:        t.Desc,
				Priority:    t.Priority.String(),
				Tags:        t.Tags,
				Due:         dateStr(t.Due),
				Start:       dateStr(t.Start),
				Scheduled:   dateStr(t.Scheduled),
				Created:     dateStr(t.Created),
				Completed:   dateStr(t.Completed),
				CancelledOn: dateStr(t.CancelledOn),
				ID:          t.ID,
				BlockedBy:   t.BlockedBy,
				Recurrence:  t.Recurrence,
				Hidden:      t.Hidden(today, openIDs),
			})
		}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func dateStr(d *task.Date) string {
	if d == nil {
		return ""
	}
	return d.String()
}

func matchesFilters(t *task.Task, tags []string, cutoff *task.Date) bool {
	for _, tag := range tags {
		if !t.HasTag(tag) {
			return false
		}
	}
	if cutoff != nil {
		if t.Due == nil || t.Due.Compare(*cutoff) > 0 {
			return false
		}
	}
	return true
}

// cmdPath prints the resolved absolute path and exits. It is the primitive
// `open` is built on, and it makes `bat $(tuido path oncall)` work.
func cmdPath(args []string) error {
	fs := newFlagSet("path")
	fl := registerScope(fs)
	if err := fs.Parse(args); err != nil {
		return flagErr(err)
	}
	all := fl.all
	a, err := openApp(*fl.root, *fl.ws)
	if err != nil {
		return err
	}
	l, err := a.resolveList(strings.Join(fs.Args(), " "), *all)
	if err != nil {
		return err
	}
	fmt.Println(l.Path)
	return nil
}

// resolveList narrows a list query to one file, offering the picker when it can.
func (a *app) resolveList(query string, all bool) (store.List, error) {
	lists, err := a.st.FindLists(query, all)
	if err != nil {
		return store.List{}, err
	}
	switch len(lists) {
	case 0:
		return store.List{}, uerr("no list matches %q", query)
	case 1:
		return lists[0], nil
	}
	if !match.Interactive() {
		return store.List{}, &store.ErrAmbiguousList{Query: query, Candidates: lists}
	}
	refs := make([]string, len(lists))
	byRef := map[string]store.List{}
	for i, l := range lists {
		refs[i] = l.Ref()
		byRef[l.Ref()] = l
	}
	chosen, err := match.PickString(refs, "list> ", query)
	if err != nil {
		return store.List{}, err
	}
	l, ok := byRef[chosen]
	if !ok {
		return store.List{}, &match.ErrCancelled{}
	}
	return l, nil
}

// cmdUse switches the persisted workspace, or reports the current one and where
// it came from — a stale context should be visible, not silent.
func cmdUse(args []string) error {
	fs := newFlagSet("use")
	fl := registerCommon(fs)
	if err := fs.Parse(args); err != nil {
		return flagErr(err)
	}
	a, err := openApp(*fl.root, *fl.ws)
	if err != nil {
		return err
	}

	if fs.NArg() == 0 {
		name, source := a.st.Workspace()
		if name == "" {
			return uerr("no workspace selected (available: %s)", strings.Join(a.st.Workspaces(), ", "))
		}
		fmt.Printf("%s  (from %s)\n", name, source)
		return nil
	}

	want := fs.Arg(0)
	available := a.st.Workspaces()
	for _, w := range available {
		if w == want {
			if err := store.WriteContext(want); err != nil {
				return err
			}
			fmt.Printf("✓ workspace → %s\n", want)
			return nil
		}
	}
	return uerr("no workspace %q (have: %s)", want, strings.Join(available, ", "))
}

// cmdNames feeds the zsh completion. It prints one name per line and stays
// silent when tuido is not set up, so completion never spews errors into the
// prompt.
func cmdNames(which string, args []string) error {
	st, err := store.Open(store.Options{})
	if err != nil {
		return nil
	}
	if which == "_workspaces" {
		for _, w := range st.Workspaces() {
			fmt.Println(w)
		}
		return nil
	}
	ws, _ := st.Workspace()
	for _, l := range st.Lists(ws) {
		fmt.Println(l.Name)
	}
	for _, l := range st.AllLists() {
		if l.Workspace != ws {
			fmt.Println(l.Ref())
		}
	}
	return nil
}

// cmdShow is the fzf preview helper: print the block around a locator.
func cmdShow(args []string) error {
	if len(args) != 1 {
		return uerr("usage: tuido show <path:line>")
	}
	path, line, err := match.ParseLocator(args[0])
	if err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	f, err := task.Parse(path, b)
	if err != nil {
		return err
	}
	idx := line - 1
	if idx < 0 || idx >= len(f.Lines) {
		return uerr("no line %d in %s", line, path)
	}

	start, end := idx, idx
	for _, b := range f.Blocks {
		if b.Start <= idx && idx <= b.End {
			start, end = b.Start, b.End
		}
	}
	// A little context above the block, so the heading is visible.
	for start > 0 && f.Lines[start-1].Kind != task.LineTask && start > idx-6 {
		start--
	}

	fmt.Println(path)
	fmt.Println()
	for i := start; i <= end && i < len(f.Lines); i++ {
		marker := "  "
		if i == idx {
			marker = "▸ "
		}
		fmt.Println(marker + f.Lines[i].Raw)
	}
	return nil
}

// cmdSync is the blocking, verbose, on-demand counterpart to the background job.
func cmdSync(args []string) error {
	fs := newFlagSet("sync")
	fl := registerSync(fs)
	if err := fs.Parse(args); err != nil {
		return flagErr(err)
	}
	status := fl.status
	a, err := openApp(*fl.root, *fl.ws)
	if err != nil {
		return err
	}

	if *status {
		s := a.repo.ReadState()
		fmt.Printf("root      %s\n", a.st.Root)
		fmt.Printf("git       %v\n", a.st.Cfg.Git.Enabled)
		fmt.Printf("unpushed  %d\n", s.Unpushed)
		fmt.Printf("behind    %d\n", s.Behind)
		fmt.Printf("diverged  %v\n", s.Diverged)
		if s.LastFetch > 0 {
			fmt.Printf("fetched   %s ago\n", ago(s.LastFetch))
		}
		if s.LastError != "" {
			fmt.Printf("error     %s\n", s.LastError)
		}
		return nil
	}

	res, err := a.repo.Sync()
	if err != nil {
		return err
	}
	for _, m := range res.Messages {
		fmt.Println("· " + m)
	}
	if len(res.Messages) > 0 {
		return nil
	}
	if res.Rebased {
		fmt.Println("✓ rebased onto origin")
	}
	if res.Pushed > 0 {
		fmt.Printf("✓ pushed %d commit(s)\n", res.Pushed)
	}
	if !res.Rebased && res.Pushed == 0 {
		fmt.Println("✓ already up to date")
	}
	return nil
}

// cmdInternalSync is the detached background job. It is undocumented on
// purpose: nothing but tuido should invoke it.
func cmdInternalSync(args []string) error {
	fs := pflag.NewFlagSet("internal-sync", pflag.ContinueOnError)
	push := fs.Bool("push", false, "push only")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := store.Open(store.Options{})
	if err != nil {
		return err
	}
	repo := vcsFor(st)
	if *push {
		return repo.Push()
	}
	return repo.Background()
}
