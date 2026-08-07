package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ramtinJ95/tuido/internal/task"
)

// setup points XDG at a temp dir and builds a small repo, so no test touches
// the real config.
func setup(t *testing.T) *Store {
	t.Helper()
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	t.Setenv("TUIDO_WORKSPACE", "")
	t.Setenv("TUIDO_ROOT", "")

	root := filepath.Join(home, "todo")
	for _, f := range []struct{ path, body string }{
		{"work/inbox.md", "- [ ] alpha\n"},
		{"work/oncall.md", "# now\n\n- [ ] beta\n\n# next\n\n- [ ] gamma\n"},
		{"work/archive/2026.md", "- [x] old ✅ 2026-01-01\n"},
		{"personal/inbox.md", "- [ ] delta\n"},
	} {
		p := filepath.Join(root, filepath.FromSlash(f.path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := DefaultConfig()
	cfg.Root = root
	cfg.DefaultWorkspace = "work"
	cfg.Git.Enabled = false
	if err := SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}

	s, err := Open(Options{})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestWorkspacesAndLists(t *testing.T) {
	s := setup(t)
	if got := strings.Join(s.Workspaces(), ","); got != "personal,work" {
		t.Errorf("workspaces = %q", got)
	}
	var names []string
	for _, l := range s.Lists("work") {
		names = append(names, l.Name)
	}
	if got := strings.Join(names, ","); got != "archive/2026,inbox,oncall" {
		t.Errorf("lists = %q (nested lists must be found)", got)
	}
}

func TestWorkspacePrecedence(t *testing.T) {
	s := setup(t)
	if ws, src := s.Workspace(); ws != "work" || src != "config" {
		t.Errorf("workspace = %q from %q, want work from config", ws, src)
	}

	if err := WriteContext("personal"); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if ws, src := s2.Workspace(); ws != "personal" || src != "context" {
		t.Errorf("workspace = %q from %q, want personal from context", ws, src)
	}

	// The flag wins over everything, which is the mitigation for a stale context.
	s3, err := Open(Options{Workspace: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if ws, src := s3.Workspace(); ws != "work" || src != "flag" {
		t.Errorf("workspace = %q from %q, want work from flag", ws, src)
	}

	t.Setenv("TUIDO_WORKSPACE", "personal")
	s4, err := Open(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, src := s4.Workspace(); src != "TUIDO_WORKSPACE" {
		t.Errorf("env did not beat context: source = %q", src)
	}
}

func TestRootPrecedence(t *testing.T) {
	s := setup(t)
	other := t.TempDir()
	t.Setenv("TUIDO_ROOT", other)
	s2, err := Open(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s2.Root != other {
		t.Errorf("env root = %q, want %q", s2.Root, other)
	}

	third := t.TempDir()
	s3, err := Open(Options{Root: third})
	if err != nil {
		t.Fatal(err)
	}
	if s3.Root != third {
		t.Errorf("flag root = %q, want %q", s3.Root, third)
	}
	if s.Root == other {
		t.Error("config root should not have changed")
	}
}

func TestFindListsPrefersExactMatch(t *testing.T) {
	s := setup(t)
	// "inbox" exists in both workspaces; scoped to `work` there is one.
	lists, err := s.FindLists("inbox", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(lists) != 1 || lists[0].Ref() != "work/inbox" {
		t.Fatalf("lists = %v", refs(lists))
	}

	// Across workspaces it is genuinely ambiguous.
	lists, _ = s.FindLists("inbox", true)
	if len(lists) != 2 {
		t.Errorf("--all lists = %v, want both inboxes", refs(lists))
	}

	// A fragment matches by substring.
	lists, _ = s.FindLists("onc", false)
	if len(lists) != 1 || lists[0].Name != "oncall" {
		t.Errorf("fragment match = %v", refs(lists))
	}

	// An exact name must not be shadowed by a longer substring match.
	lists, _ = s.FindLists("archive/2026", false)
	if len(lists) != 1 || lists[0].Name != "archive/2026" {
		t.Errorf("nested exact = %v", refs(lists))
	}
}

func TestResolveTargetSectionFallback(t *testing.T) {
	s := setup(t)

	tgt, err := s.ResolveTarget("oncall", false)
	if err != nil || tgt.Section != "" {
		t.Fatalf("target = %+v, err = %v", tgt, err)
	}

	tgt, err = s.ResolveTarget("oncall/next", false)
	if err != nil {
		t.Fatal(err)
	}
	if tgt.List.Name != "oncall" || tgt.Section != "next" {
		t.Errorf("target = %+v, want oncall/next", tgt)
	}

	// A real nested file wins over reading the tail as a section.
	tgt, err = s.ResolveTarget("archive/2026", false)
	if err != nil {
		t.Fatal(err)
	}
	if tgt.Section != "" || tgt.List.Name != "archive/2026" {
		t.Errorf("nested list read as a section: %+v", tgt)
	}

	if _, err := s.ResolveTarget("nope", false); err == nil {
		t.Error("unknown list did not error")
	}
}

func TestInsertPointRules(t *testing.T) {
	s := setup(t)

	// Unstructured: append at the end of the file.
	f, err := s.Read(List{Path: filepath.Join(s.Root, "work", "inbox.md")})
	if err != nil {
		t.Fatal(err)
	}
	idx, err := InsertPoint(f, "")
	if err != nil {
		t.Fatal(err)
	}
	if idx != 1 {
		t.Errorf("insert point = %d, want 1 (after the single task)", idx)
	}

	// Structured with no section named and no marker: refuse, and say what the
	// choices are.
	f2, err := s.Read(List{Path: filepath.Join(s.Root, "work", "oncall.md")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InsertPoint(f2, ""); err == nil {
		t.Fatal("structured capture did not refuse")
	} else {
		var ns *ErrNeedsSection
		if !asErr(err, &ns) {
			t.Fatalf("want ErrNeedsSection, got %T", err)
		}
		if strings.Join(ns.Sections, ",") != "now,next" {
			t.Errorf("sections = %v", ns.Sections)
		}
	}

	// Naming a section lands at the end of that section's last block.
	idx, err = InsertPoint(f2, "now")
	if err != nil {
		t.Fatal(err)
	}
	if f2.Lines[idx-1].Raw != "- [ ] beta" {
		t.Errorf("insert point %d follows %q, want the `beta` line", idx, f2.Lines[idx-1].Raw)
	}

	if _, err := InsertPoint(f2, "nonexistent"); err == nil {
		t.Error("unknown section did not error")
	}
}

func TestAddAppendsAndPreservesTheRest(t *testing.T) {
	s := setup(t)
	tgt, err := s.ResolveTarget("oncall/next", false)
	if err != nil {
		t.Fatal(err)
	}
	k := &task.Task{Desc: "new thing", Bullet: "-"}
	if err := s.Add(tgt, k); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(tgt.List.Path)
	if err != nil {
		t.Fatal(err)
	}
	want := "# now\n\n- [ ] beta\n\n# next\n\n- [ ] gamma\n- [ ] new thing\n"
	if string(body) != want {
		t.Errorf("file =\n%q\nwant\n%q", body, want)
	}
}

// A file whose last line has no trailing newline must gain one before anything
// is appended, or two tasks end up on the same line.
func TestAddToFileWithoutTrailingNewline(t *testing.T) {
	s := setup(t)
	p := filepath.Join(s.Root, "work", "nonl.md")
	if err := os.WriteFile(p, []byte("- [ ] only"), 0o644); err != nil {
		t.Fatal(err)
	}
	tgt := Target{List: List{Workspace: "work", Name: "nonl", Path: p}}
	if err := s.Add(tgt, &task.Task{Desc: "second", Bullet: "-"}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(p)
	if string(body) != "- [ ] only\n- [ ] second\n" {
		t.Errorf("file = %q", body)
	}
}

func TestWriteIsAtomicAndKeepsMode(t *testing.T) {
	s := setup(t)
	p := filepath.Join(s.Root, "work", "inbox.md")
	if err := os.Chmod(p, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := s.Read(List{Path: p})
	if err != nil {
		t.Fatal(err)
	}
	f.Tasks()[0].SetState(task.Done)
	if err := s.Write(f); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	// No temp files left behind.
	entries, _ := os.ReadDir(filepath.Dir(p))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tuido-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestOpenIDsOnlyCountsOpenTasks(t *testing.T) {
	s := setup(t)
	p := filepath.Join(s.Root, "work", "ids.md")
	body := "- [ ] open one 🆔 aaa\n- [x] closed one 🆔 bbb ✅ 2026-01-01\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	ids := s.OpenIDs(s.Lists("work"))
	if !ids["aaa"] {
		t.Error("open id missing")
	}
	if ids["bbb"] {
		t.Error("closed task's id counted as blocking")
	}
}

func TestNotInitialised(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	t.Setenv("TUIDO_ROOT", "")
	if _, err := Open(Options{}); err == nil {
		t.Fatal("want ErrNotInitialised")
	} else if !strings.Contains(err.Error(), "tuido init") {
		t.Errorf("err = %v", err)
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	got, err := ExpandPath("~/notes/todo")
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join(home, "notes", "todo") {
		t.Errorf("ExpandPath = %q", got)
	}
	if _, err := ExpandPath(""); err == nil {
		t.Error("empty path did not error")
	}
}

func refs(ls []List) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		out[i] = l.Ref()
	}
	return out
}

func asErr[T error](err error, target *T) bool {
	for err != nil {
		if v, ok := err.(T); ok {
			*target = v
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
