package store

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ramtinJ95/tuido/internal/task"
)

// archiveDir is where a workspace's archived tasks live. It is deliberately
// not dot-prefixed: the archive should be visible in editors, `ls` and on
// GitHub, at the cost of the explicit skip in Lists.
const archiveDir = "_archive"

// ArchivePath is the mirror file that receives a list's archived tasks:
// work/inbox.md → work/_archive/inbox.md, nested lists keep their subpath.
func (s *Store) ArchivePath(l List) string {
	return filepath.Join(s.Root, l.Workspace, archiveDir, filepath.FromSlash(l.Name)+".md")
}

// ArchiveResult reports one list's sweep.
type ArchiveResult struct {
	Moved   int
	Skipped []string // closed items left in place, with the reason
	Archive string   // destination path
}

// ArchiveClosed moves every fully-closed top-level item of l into the list's
// archive mirror, recreating the item's heading path there. Lines move
// byte-for-byte — the whole item range, continuation lines and subtree
// included — so nothing is re-serialised and Rewritable does not apply.
//
// A closed item with an open descendant is skipped and reported: archiving it
// would make open work invisible. With dryRun the selection is reported and
// nothing is written.
func (s *Store) ArchiveClosed(l List, dryRun bool) (ArchiveResult, error) {
	res := ArchiveResult{Archive: s.ArchivePath(l)}
	f, err := s.Read(l)
	if err != nil {
		return res, err
	}
	paths := f.HeadingPaths()

	type move struct {
		item task.Item
		path []task.Heading
	}
	var moves []move
	for _, b := range f.Blocks {
		for _, it := range b.Items {
			if !it.Task.State.Closed() {
				continue
			}
			if open := openDescendant(f, it); open != nil {
				res.Skipped = append(res.Skipped,
					fmt.Sprintf("%s — open subtask: %s", it.Task.Desc, open.Desc))
				continue
			}
			moves = append(moves, move{it, paths[it.Task.Line]})
		}
	}
	res.Moved = len(moves)
	if len(moves) == 0 || dryRun {
		return res, nil
	}

	// Archive first, source second: a crash between the two writes leaves a
	// duplicate, never a loss.
	af, err := readArchive(res.Archive)
	if err != nil {
		return res, err
	}
	for _, m := range moves {
		if af, err = insertArchived(af, m.path, f, m.item); err != nil {
			return res, err
		}
	}
	if err := writeAtomic(res.Archive, af.Bytes(), 0o644); err != nil {
		return res, err
	}

	// Descending order keeps the earlier items' line indices valid.
	for i := len(moves) - 1; i >= 0; i-- {
		start := moves[i].item.Start
		f.RemoveLines(start, moves[i].item.End)
		// A removed block bordered by blank lines would leave two in a row,
		// one more per sweep; collapsing the pair is the only non-task line
		// archiving ever touches.
		if start > 0 && start < len(f.Lines) &&
			f.Lines[start-1].Kind == task.LineBlank && f.Lines[start].Kind == task.LineBlank {
			f.RemoveLines(start, start)
		}
	}
	if err := s.Write(f); err != nil {
		return res, err
	}
	return res, nil
}

// openDescendant returns the first still-open task inside the item's range, or
// nil when the whole subtree is closed.
func openDescendant(f *task.File, it task.Item) *task.Task {
	for i := it.Start; i <= it.End; i++ {
		if t := f.Lines[i].Task; t != nil && !t.State.Closed() {
			return t
		}
	}
	return nil
}

// readArchive parses the archive mirror, treating a missing file as empty. A
// conflicted archive refuses the sweep through the normal ErrConflicted path.
func readArchive(path string) (*task.File, error) {
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return task.Parse(path, b)
}

// insertArchived returns af with one item spliced in under its heading path.
// The result is reparsed so the next insertion works from real classification
// (fences included) rather than hand-maintained state.
func insertArchived(af *task.File, path []task.Heading, src *task.File, it task.Item) (*task.File, error) {
	eol := af.EOL()
	if len(af.Lines) == 0 {
		eol = src.EOL() // a brand-new archive inherits the source's endings
	}

	idx, matched := archiveInsertPoint(af, path)

	var raws []string
	if matched < len(path) {
		// Created headings are the source's raw heading lines, verbatim, with
		// one blank above when they would otherwise touch existing content.
		if idx > 0 && af.Lines[idx-1].Kind != task.LineBlank {
			raws = append(raws, "")
		}
		for _, h := range path[matched:] {
			raws = append(raws, src.Lines[h.Line-1].Raw)
		}
	}
	for i := it.Start; i <= it.End; i++ {
		raws = append(raws, src.Lines[i].Raw)
	}

	var buf bytes.Buffer
	for i, ln := range af.Lines {
		if i == idx {
			for _, r := range raws {
				buf.WriteString(r)
				buf.WriteString(eol)
			}
		}
		buf.WriteString(ln.Raw)
		if ln.EOL == "" && idx == len(af.Lines) {
			// Appending after a final line with no terminator needs one first.
			buf.WriteString(eol)
		} else {
			buf.WriteString(ln.EOL)
		}
	}
	if idx == len(af.Lines) {
		for _, r := range raws {
			buf.WriteString(r)
			buf.WriteString(eol)
		}
	}
	return task.Parse(af.Path, buf.Bytes())
}

// archiveInsertPoint finds where an item with the given heading path goes:
// idx is the File.Lines index to insert before, matched is how many leading
// path components already exist in the archive.
//
// Full path present → right after the last non-blank line whose active heading
// path is exactly the target: the end of that section's direct content, above
// any subheadings. Otherwise → the end of the longest matched prefix's whole
// section (end of file for a prefix of zero), where the missing headings are
// created. An empty path targets the top-of-file region, which keeps a marker
// comment first.
func archiveInsertPoint(af *task.File, path []task.Heading) (idx, matched int) {
	lastExact := -1
	lastWithin := make([]int, len(path)+1)
	for k := range lastWithin {
		lastWithin[k] = -1
	}

	// Active heading tracking mirrors HeadingPaths, including the collapse of
	// missing levels; matching is trimmed and case-insensitive, like
	// SectionRange.
	var levels [6]*string
	for i, ln := range af.Lines {
		if ln.Kind == task.LineHeading {
			lv := task.HeadingLevel(ln.Raw)
			txt := task.HeadingText(ln.Raw)
			levels[lv-1] = &txt
			for n := lv; n < len(levels); n++ {
				levels[n] = nil
			}
		}
		if ln.Kind == task.LineBlank {
			continue
		}
		var cur []string
		for _, h := range levels {
			if h != nil && *h != "" {
				cur = append(cur, *h)
			}
		}
		m := 0
		for m < len(cur) && m < len(path) && strings.EqualFold(cur[m], path[m].Text) {
			m++
		}
		if m == len(path) && len(cur) == len(path) {
			lastExact = i
		}
		for k := 0; k <= m; k++ {
			lastWithin[k] = i
		}
	}

	if lastExact >= 0 || len(path) == 0 {
		return lastExact + 1, len(path)
	}
	for k := len(path) - 1; k >= 0; k-- {
		if lastWithin[k] >= 0 {
			return lastWithin[k] + 1, k
		}
	}
	return 0, 0
}

// ArchivedLists returns every archive mirror in a workspace. Refs print
// honestly as workspace/_archive/list.
func (s *Store) ArchivedLists(ws string) []List {
	dir := filepath.Join(s.Root, ws, archiveDir)
	var out []List
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && p != dir {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		out = append(out, List{
			Workspace: ws,
			Name:      archiveDir + "/" + strings.TrimSuffix(filepath.ToSlash(rel), ".md"),
			Path:      p,
		})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// FindArchivedLists is FindLists over the archive mirrors instead of the live
// lists — same workspace scoping, same matching.
func (s *Store) FindArchivedLists(query string, all bool) ([]List, error) {
	var scope []List
	if all || s.workspace == "" {
		for _, ws := range s.Workspaces() {
			scope = append(scope, s.ArchivedLists(ws)...)
		}
	} else {
		if _, err := s.WorkspaceDir(); err != nil {
			return nil, err
		}
		scope = s.ArchivedLists(s.workspace)
	}
	return matchLists(scope, query), nil
}
