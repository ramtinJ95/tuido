package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ramtinJ95/tuido/internal/task"
)

// Store is a resolved view of the task repo for one command invocation.
type Store struct {
	Root string
	Cfg  Config

	workspace       string
	workspaceSource string
}

// Options are the per-invocation overrides from flags.
type Options struct {
	Root      string // --root
	Workspace string // -w
}

// Open resolves the root and the current workspace.
//
// Root precedence: --root > TUIDO_ROOT > config.
// Workspace precedence: -w > TUIDO_WORKSPACE > context file > config.
func Open(opt Options) (*Store, error) {
	cfg, err := LoadConfig()
	switch {
	case errors.Is(err, ErrNotInitialised):
		// A root supplied on the command line or in the environment is enough
		// to work without a config file at all.
		if opt.Root == "" && os.Getenv("TUIDO_ROOT") == "" {
			return nil, ErrNotInitialised
		}
		cfg = DefaultConfig()
	case err != nil:
		return nil, err
	}

	root := firstNonEmpty(opt.Root, os.Getenv("TUIDO_ROOT"), cfg.Root)
	if root == "" {
		return nil, ErrNotInitialised
	}
	abs, err := ExpandPath(root)
	if err != nil {
		return nil, err
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return nil, fmt.Errorf("todo root %s is not a directory — run `tuido init`", abs)
	}
	s := &Store{Root: abs, Cfg: cfg}

	ws, src := firstNonEmptyLabelled(
		labelled{opt.Workspace, "flag"},
		labelled{os.Getenv("TUIDO_WORKSPACE"), "TUIDO_WORKSPACE"},
		labelled{ReadContext(), "context"},
		labelled{cfg.DefaultWorkspace, "config"},
	)
	if ws == "" {
		// One workspace and no preference expressed is not ambiguous.
		if all := s.Workspaces(); len(all) == 1 {
			ws, src = all[0], "only workspace"
		}
	}
	s.workspace, s.workspaceSource = ws, src
	return s, nil
}

// Workspace is the resolved workspace name, and where it came from. Commands
// print the source when it matters, so a stale persisted context is visible
// rather than silent.
func (s *Store) Workspace() (name, source string) { return s.workspace, s.workspaceSource }

// WorkspaceDir is the absolute directory for the current workspace.
func (s *Store) WorkspaceDir() (string, error) {
	if s.workspace == "" {
		return "", errors.New("no workspace selected — use `tuido use <workspace>` or -w")
	}
	dir := filepath.Join(s.Root, s.workspace)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("workspace %q does not exist in %s", s.workspace, s.Root)
	}
	return dir, nil
}

// Workspaces lists the workspace directories under the root.
func (s *Store) Workspaces() []string {
	entries, err := os.ReadDir(s.Root)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// List identifies one markdown file.
type List struct {
	Workspace string
	Name      string // path under the workspace, without .md
	Path      string
}

// Ref is the human-readable `workspace/list` identifier.
func (l List) Ref() string { return l.Workspace + "/" + l.Name }

// Lists returns every list in a workspace, sorted.
func (s *Store) Lists(ws string) []List {
	dir := filepath.Join(s.Root, ws)
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
			Name:      strings.TrimSuffix(filepath.ToSlash(rel), ".md"),
			Path:      p,
		})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// AllLists returns every list in every workspace.
func (s *Store) AllLists() []List {
	var out []List
	for _, ws := range s.Workspaces() {
		out = append(out, s.Lists(ws)...)
	}
	return out
}

// Scope is the set of lists a command operates on.
func (s *Store) Scope(all bool) ([]List, error) {
	if all {
		return s.AllLists(), nil
	}
	if s.workspace == "" {
		return s.AllLists(), nil
	}
	if _, err := s.WorkspaceDir(); err != nil {
		return nil, err
	}
	return s.Lists(s.workspace), nil
}

// FindLists returns every list whose ref contains all of the query's tokens.
// An exact ref or name match wins outright, which is what makes `tuido ls
// inbox` unambiguous even when several workspaces have an inbox in scope.
func (s *Store) FindLists(query string, all bool) ([]List, error) {
	scope, err := s.Scope(all)
	if err != nil {
		return nil, err
	}
	if query == "" {
		return scope, nil
	}
	q := strings.ToLower(strings.TrimSpace(query))

	var exact []List
	for _, l := range scope {
		if strings.ToLower(l.Ref()) == q || strings.ToLower(l.Name) == q {
			exact = append(exact, l)
		}
	}
	if len(exact) > 0 {
		return exact, nil
	}

	tokens := strings.Fields(strings.ReplaceAll(q, "/", " "))
	var out []List
	for _, l := range scope {
		hay := strings.ToLower(l.Ref())
		ok := true
		for _, tok := range tokens {
			if !strings.Contains(hay, tok) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, l)
		}
	}
	return out, nil
}

// Read parses a list. A conflicted file is refused here, once, so no command
// has to remember to check.
func (s *Store) Read(l List) (*task.File, error) {
	b, err := os.ReadFile(l.Path)
	if err != nil {
		return nil, err
	}
	return task.Parse(l.Path, b)
}

// Write commits a parsed file back to disk atomically. It is a no-op when
// nothing changed, so commands can call it unconditionally.
func (s *Store) Write(f *task.File) error {
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(f.Path); err == nil {
		mode = fi.Mode().Perm()
	}
	return writeAtomic(f.Path, f.Bytes(), mode)
}

// OpenIDs is the set of 🆔 values belonging to tasks that are still open
// anywhere in scope. It is rebuilt per invocation — one pass over the files, no
// index, no cache.
func (s *Store) OpenIDs(scope []List) map[string]bool {
	ids := map[string]bool{}
	for _, l := range scope {
		f, err := s.Read(l)
		if err != nil {
			continue
		}
		for _, t := range f.Tasks() {
			if t.ID != "" && !t.State.Closed() {
				ids[t.ID] = true
			}
		}
	}
	return ids
}

// writeAtomic replaces a file via a temp file in the same directory, so a
// crash mid-write can never leave a half-written task list.
func writeAtomic(path string, b []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tuido-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

type labelled struct{ value, label string }

func firstNonEmptyLabelled(in ...labelled) (string, string) {
	for _, l := range in {
		if l.value != "" {
			return l.value, l.label
		}
	}
	return "", ""
}

func firstNonEmpty(in ...string) string {
	for _, s := range in {
		if s != "" {
			return s
		}
	}
	return ""
}
