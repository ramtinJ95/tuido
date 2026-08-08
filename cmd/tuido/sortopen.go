package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/ramtinJ95/tuido/internal/match"
	"github.com/ramtinJ95/tuido/internal/sortkey"
	"github.com/ramtinJ95/tuido/internal/store"
	"github.com/ramtinJ95/tuido/internal/task"
)

// cmdSort reorders tasks within their blocks. It never crosses a fence, and
// running it twice produces identical bytes.
func cmdSort(args []string) error {
	fs := newFlagSet("sort")
	fl := registerSort(fs)
	if err := fs.Parse(args); err != nil {
		return flagErr(err)
	}
	by, all := fl.by, fl.all
	if *by != "" {
		if _, err := sortkey.ParseMode(*by); err != nil {
			return uerr("%v", err)
		}
	}

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
	openIDs := a.st.OpenIDs(lists)

	touched := 0
	for _, l := range lists {
		f, err := a.st.Read(l)
		if err != nil {
			return err
		}
		mode, err := sortkey.ModeFor(f, sortkey.Mode(*by))
		if err != nil {
			return uerr("%s: %v", l.Ref(), err)
		}
		if mode == sortkey.None {
			continue
		}

		moved := sortkey.File(f, mode, openIDs)
		if moved == 0 {
			// Say so explicitly. A file full of tasks that never moves is the
			// signature of a loose list, where every blank line is a fence.
			if len(f.Tasks()) > 2 && countSortable(f) == 0 {
				fmt.Printf("· %s: %d tasks, none in a sortable block (blank lines separate them)\n",
					l.Ref(), len(f.Tasks()))
			}
			continue
		}
		if err := a.st.Write(f); err != nil {
			return err
		}
		touched++
		fmt.Printf("✓ sorted %s (%d moved, by %s)\n", l.Ref(), moved, mode)
		a.commit(l.Path, fmt.Sprintf("sort: %s by %s", l.Ref(), mode))
	}
	if touched == 0 {
		fmt.Println("· already sorted")
	}
	return nil
}

// countSortable counts blocks with more than one item — the only blocks
// sorting can affect.
func countSortable(f *task.File) int {
	n := 0
	for _, b := range f.Blocks {
		if len(b.Items) > 1 {
			n++
		}
	}
	return n
}

// cmdOpen drops into the editor: a list, or the whole repo.
func cmdOpen(args []string) error {
	// Note: `open` spends --root on the documented boolean, so overriding the
	// todo root for this command means TUIDO_ROOT.
	fs := newFlagSet("open")
	fl := registerOpen(fs)
	if err := fs.Parse(args); err != nil {
		return flagErr(err)
	}
	wholeRepo, all := fl.wholeRepo, fl.all

	a, err := openApp("", *fl.ws)
	if err != nil {
		return err
	}
	if *wholeRepo {
		return editAt(a.st.Root, a.st.Root)
	}

	query := strings.Join(fs.Args(), " ")
	lists, err := a.st.FindLists(query, *all)
	if err != nil {
		return err
	}
	switch {
	case len(lists) == 1:
		return editAt(a.st.Root, lists[0].Path)
	case len(lists) == 0:
		return uerr("no list matches %q", query)
	}

	if !match.Interactive() {
		return &store.ErrAmbiguousList{Query: query, Candidates: lists}
	}
	l, err := a.resolveList(query, *all)
	if err != nil {
		return err
	}
	return editAt(a.st.Root, l.Path)
}

// editAt runs the editor with cwd at the repo root, so telescope and grep are
// scoped to the whole task repo rather than to one file's directory.
func editAt(root, target string) error {
	ed := editorCommand()
	cmd := exec.Command(ed[0], append(ed[1:], target)...)
	cmd.Dir = root
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
