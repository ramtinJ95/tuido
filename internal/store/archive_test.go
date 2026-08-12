package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeList replaces a list's file and returns its List.
func writeList(t *testing.T, s *Store, ws, name, body string) List {
	t.Helper()
	p := filepath.Join(s.Root, ws, filepath.FromSlash(name)+".md")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return List{Workspace: ws, Name: name, Path: p}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestArchiveClosedRecreatesHeadings(t *testing.T) {
	s := setup(t)
	l := writeList(t, s, "work", "inbox",
		"# Errands\n\n- [x] renew passport ✅ 2026-07-02\n- [ ] buy stamps\n\n## Sub\n\n- [-] cancelled thing ❌ 2026-07-03\n")

	res, err := s.ArchiveClosed(l, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved != 2 || len(res.Skipped) != 0 {
		t.Fatalf("moved %d skipped %v, want 2 moved", res.Moved, res.Skipped)
	}

	wantSrc := "# Errands\n\n- [ ] buy stamps\n\n## Sub\n\n"
	if got := readFile(t, l.Path); got != wantSrc {
		t.Errorf("source = %q, want %q", got, wantSrc)
	}
	wantArc := "# Errands\n- [x] renew passport ✅ 2026-07-02\n\n## Sub\n- [-] cancelled thing ❌ 2026-07-03\n"
	if got := readFile(t, res.Archive); got != wantArc {
		t.Errorf("archive = %q, want %q", got, wantArc)
	}
}

func TestArchiveClosedMergesIntoExistingSection(t *testing.T) {
	s := setup(t)
	l := writeList(t, s, "work", "inbox",
		"# Errands\n\n- [x] first ✅ 2026-07-01\n- [ ] second\n")
	if _, err := s.ArchiveClosed(l, false); err != nil {
		t.Fatal(err)
	}

	writeList(t, s, "work", "inbox", "# Errands\n\n- [x] second ✅ 2026-07-05\n")
	res, err := s.ArchiveClosed(l, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved != 1 {
		t.Fatalf("moved %d, want 1", res.Moved)
	}
	// The second sweep appends into the existing section, not a new heading.
	want := "# Errands\n- [x] first ✅ 2026-07-01\n- [x] second ✅ 2026-07-05\n"
	if got := readFile(t, res.Archive); got != want {
		t.Errorf("archive = %q, want %q", got, want)
	}
}

func TestArchiveClosedSkipsOpenSubtasks(t *testing.T) {
	s := setup(t)
	l := writeList(t, s, "work", "inbox",
		"- [x] parent ✅ 2026-08-01\n  - [ ] child still open\n- [x] solo ✅ 2026-08-01\n")

	res, err := s.ArchiveClosed(l, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved != 1 || len(res.Skipped) != 1 {
		t.Fatalf("moved %d skipped %v, want 1 and 1", res.Moved, res.Skipped)
	}
	if !strings.Contains(res.Skipped[0], "open subtask: child still open") {
		t.Errorf("skip reason = %q", res.Skipped[0])
	}
	wantSrc := "- [x] parent ✅ 2026-08-01\n  - [ ] child still open\n"
	if got := readFile(t, l.Path); got != wantSrc {
		t.Errorf("source = %q, want %q", got, wantSrc)
	}
	if got := readFile(t, res.Archive); got != "- [x] solo ✅ 2026-08-01\n" {
		t.Errorf("archive = %q", got)
	}
}

func TestArchiveClosedCollapsesDoubleBlank(t *testing.T) {
	s := setup(t)
	l := writeList(t, s, "work", "oncall",
		"# now\n\n- [x] beta ✅ 2026-08-01\n\n# next\n\n- [ ] gamma\n")

	if _, err := s.ArchiveClosed(l, false); err != nil {
		t.Fatal(err)
	}
	want := "# now\n\n# next\n\n- [ ] gamma\n"
	if got := readFile(t, l.Path); got != want {
		t.Errorf("source = %q, want %q", got, want)
	}
}

func TestArchiveClosedDryRunWritesNothing(t *testing.T) {
	s := setup(t)
	body := "- [x] done thing ✅ 2026-08-01\n- [ ] open thing\n"
	l := writeList(t, s, "work", "inbox", body)

	res, err := s.ArchiveClosed(l, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved != 1 {
		t.Fatalf("moved %d, want 1", res.Moved)
	}
	if got := readFile(t, l.Path); got != body {
		t.Errorf("dry run touched the source: %q", got)
	}
	if _, err := os.Stat(res.Archive); !os.IsNotExist(err) {
		t.Errorf("dry run created the archive: %v", err)
	}
}

func TestArchiveClosedSecondSweepIsNoop(t *testing.T) {
	s := setup(t)
	l := writeList(t, s, "work", "inbox", "- [x] once ✅ 2026-08-01\n- [ ] stays\n")
	if _, err := s.ArchiveClosed(l, false); err != nil {
		t.Fatal(err)
	}
	arc := readFile(t, s.ArchivePath(l))
	src := readFile(t, l.Path)

	res, err := s.ArchiveClosed(l, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Moved != 0 {
		t.Fatalf("second sweep moved %d", res.Moved)
	}
	if readFile(t, s.ArchivePath(l)) != arc || readFile(t, l.Path) != src {
		t.Error("second sweep changed the files")
	}
}

func TestArchiveMirrorsNestedListsAndStaysOutOfScope(t *testing.T) {
	s := setup(t)
	// The fixture's work/archive/2026.md is a real, visible list whose name
	// merely resembles the feature; it must mirror under _archive/archive/.
	l := List{Workspace: "work", Name: "archive/2026",
		Path: filepath.Join(s.Root, "work", "archive", "2026.md")}

	res, err := s.ArchiveClosed(l, false)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(s.Root, "work", archiveDir, "archive", "2026.md")
	if res.Archive != want {
		t.Fatalf("archive path = %q, want %q", res.Archive, want)
	}
	if got := readFile(t, want); got != "- [x] old ✅ 2026-01-01\n" {
		t.Errorf("archive = %q", got)
	}

	var names []string
	for _, li := range s.Lists("work") {
		names = append(names, li.Name)
	}
	if got := strings.Join(names, ","); got != "archive/2026,inbox,oncall" {
		t.Errorf("lists after sweep = %q — _archive must stay out of scope", got)
	}

	var arch []string
	for _, li := range s.ArchivedLists("work") {
		arch = append(arch, li.Name)
	}
	if got := strings.Join(arch, ","); got != "_archive/archive/2026" {
		t.Errorf("archived lists = %q", got)
	}
}

func TestFindArchivedLists(t *testing.T) {
	s := setup(t)
	l := writeList(t, s, "work", "inbox", "- [x] gone ✅ 2026-08-01\n")
	if _, err := s.ArchiveClosed(l, false); err != nil {
		t.Fatal(err)
	}

	lists, err := s.FindArchivedLists("inbox", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(lists) != 1 || lists[0].Name != "_archive/inbox" {
		t.Fatalf("FindArchivedLists = %+v", lists)
	}
	if lists[0].Ref() != "work/_archive/inbox" {
		t.Errorf("ref = %q", lists[0].Ref())
	}

	// The archived workspace scope honours -w/current workspace like Lists.
	lists, err = s.FindArchivedLists("", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(lists) != 1 {
		t.Fatalf("all-workspace archived scope = %+v", lists)
	}
}
