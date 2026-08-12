package main

import (
	"fmt"
	"path/filepath"
	"strings"
)

// cmdArchive sweeps closed tasks out of the lists in scope into their
// _archive mirrors — the one deliberate exception to "task lines only move
// within their block". `done` never archives; this command is the only mover.
func cmdArchive(args []string) error {
	fs := newFlagSet("archive")
	fl := registerArchive(fs)
	if err := fs.Parse(args); err != nil {
		return flagErr(err)
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

	touched := 0
	for _, l := range lists {
		res, err := a.st.ArchiveClosed(l, *fl.dryRun)
		if err != nil {
			return err
		}
		for _, skip := range res.Skipped {
			fmt.Printf("· skipped in %s: %s\n", l.Ref(), skip)
		}
		if res.Moved == 0 {
			continue
		}
		touched++
		rel, rerr := filepath.Rel(filepath.Join(a.st.Root, l.Workspace), res.Archive)
		if rerr != nil {
			rel = res.Archive
		}
		if *fl.dryRun {
			fmt.Printf("· would archive %d from %s → %s\n", res.Moved, l.Ref(), rel)
			continue
		}
		fmt.Printf("✓ archived %d from %s → %s\n", res.Moved, l.Ref(), rel)
		a.commit(fmt.Sprintf("archive: %d from %s", res.Moved, l.Ref()), res.Archive, l.Path)
	}
	if touched == 0 {
		fmt.Println("· nothing to archive")
	}
	return nil
}
