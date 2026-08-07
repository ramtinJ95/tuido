package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ramtinJ95/tuido/internal/task"
)

// Target is a resolved destination for a new task.
type Target struct {
	List    List
	Section string // "" means end of file
}

// Ref is what a mutating command prints so the destination is never a guess.
func (t Target) Ref() string {
	if t.Section == "" {
		return t.List.Ref()
	}
	return t.List.Ref() + "/" + t.Section
}

// ErrAmbiguousList carries the candidates so the caller can offer a picker.
type ErrAmbiguousList struct {
	Query      string
	Candidates []List
}

func (e *ErrAmbiguousList) Error() string {
	refs := make([]string, len(e.Candidates))
	for i, l := range e.Candidates {
		refs[i] = l.Ref()
	}
	return fmt.Sprintf("%q matches %d lists: %s", e.Query, len(e.Candidates), strings.Join(refs, ", "))
}

// ResolveTarget turns a -l value into a destination.
//
// The spec may name a list ("oncall"), a nested list ("archive/2026"), or a
// list and a section ("oncall/now"). Lists win over sections: a real file is
// never shadowed by a heading with the same name.
func (s *Store) ResolveTarget(spec string, all bool) (Target, error) {
	if spec == "" {
		return s.defaultTarget()
	}

	if lists, err := s.FindLists(spec, all); err != nil {
		return Target{}, err
	} else if len(lists) == 1 {
		return Target{List: lists[0]}, nil
	} else if len(lists) > 1 {
		return Target{}, &ErrAmbiguousList{Query: spec, Candidates: lists}
	}

	// No list by that name; try `<list>/<section>`.
	if listPart, section, ok := cutLast(spec, "/"); ok {
		lists, err := s.FindLists(listPart, all)
		if err != nil {
			return Target{}, err
		}
		switch {
		case len(lists) == 1:
			return Target{List: lists[0], Section: section}, nil
		case len(lists) > 1:
			return Target{}, &ErrAmbiguousList{Query: listPart, Candidates: lists}
		}
	}
	return Target{}, fmt.Errorf("no list matches %q", spec)
}

// defaultTarget is the capture list of the current workspace, created on first
// use so a fresh workspace does not need a manual `touch`.
func (s *Store) defaultTarget() (Target, error) {
	dir, err := s.WorkspaceDir()
	if err != nil {
		return Target{}, err
	}
	name := s.Cfg.CaptureList
	if name == "" {
		name = "inbox"
	}
	path := filepath.Join(dir, name+".md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		// Heading-less on purpose: see cmdInit. The capture list is the one
		// file a bare `tuido add` must always be able to append to.
		if err := writeAtomic(path, nil, 0o644); err != nil {
			return Target{}, err
		}
	}
	return Target{List: List{Workspace: s.workspace, Name: name, Path: path}}, nil
}

// ErrNeedsSection is returned when a structured file has no obvious place to
// put a new task. Appending to the end of such a file would bury the task under
// whatever heading happens to be last, where it can never sort its way out — so
// this refuses and lists the choices instead.
type ErrNeedsSection struct {
	List     List
	Sections []string
}

func (e *ErrNeedsSection) Error() string {
	return fmt.Sprintf("%s has headings, so a new task needs a section: %s\n"+
		"  pick one with -l %s/<section>, or set a default in the file's marker comment:\n"+
		"  <!-- tuido: capture=%s -->",
		e.List.Ref(), strings.Join(e.Sections, ", "), e.List.Name, e.Sections[0])
}

// InsertPoint returns the line index at which a new task should be spliced in.
func InsertPoint(f *task.File, section string) (int, error) {
	sections := f.Sections()

	if section == "" {
		if len(sections) == 0 {
			return endOfFile(f), nil
		}
		if f.Marker.Capture == "" {
			return 0, &ErrNeedsSection{List: List{Name: filepath.Base(f.Path)}, Sections: sections}
		}
		section = f.Marker.Capture
	}

	start, end, ok := f.SectionRange(section)
	if !ok {
		return 0, fmt.Errorf("no section %q in %s (have: %s)", section, f.Path, strings.Join(sections, ", "))
	}

	// End of the section's last block, so a new task joins the existing run
	// rather than starting an orphan one below a blank line.
	last := -1
	for _, b := range f.Blocks {
		if b.Start >= start && b.End <= end {
			last = b.End
		}
	}
	if last >= 0 {
		return last + 1, nil
	}
	// No tasks yet: after the last non-blank line of the section.
	for i := end; i > start; i-- {
		if f.Lines[i].Kind != task.LineBlank {
			return i + 1, nil
		}
	}
	return start + 1, nil
}

// endOfFile is the index after the last non-blank line, so appending does not
// drift further down the file every time a task is added and removed.
func endOfFile(f *task.File) int {
	for i := len(f.Lines) - 1; i >= 0; i-- {
		if f.Lines[i].Kind != task.LineBlank {
			return i + 1
		}
	}
	return len(f.Lines)
}

// Fix the list reference in an ErrNeedsSection raised inside InsertPoint, which
// only knows the file path.
func (t Target) annotate(err error) error {
	var ns *ErrNeedsSection
	if e, ok := err.(*ErrNeedsSection); ok {
		ns = e
		ns.List = t.List
	}
	return err
}

// Add splices a new task into the target and writes the file.
func (s *Store) Add(t Target, k *task.Task) error {
	f, err := s.Read(t.List)
	if err != nil {
		return err
	}
	idx, err := InsertPoint(f, t.Section)
	if err != nil {
		return t.annotate(err)
	}
	f.Insert(idx, f.NewLine(k))
	return s.Write(f)
}

func cutLast(s, sep string) (before, after string, found bool) {
	i := strings.LastIndex(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}
