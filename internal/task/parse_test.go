package task

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func corpusFiles(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob("testdata/corpus/*.md")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no corpus files: %v", err)
	}
	return paths
}

// TestRoundTripCorpus is the primary quality bar: parsing and writing back must
// be byte-identical when nothing was mutated.
func TestRoundTripCorpus(t *testing.T) {
	for _, p := range corpusFiles(t) {
		t.Run(filepath.Base(p), func(t *testing.T) {
			b, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			f, err := Parse(p, b)
			if err != nil {
				t.Fatal(err)
			}
			if got := f.Bytes(); string(got) != string(b) {
				t.Fatalf("round trip changed the file\n--- want ---\n%q\n--- got ---\n%q", b, got)
			}
		})
	}
}

func TestConflictedFileIsRefused(t *testing.T) {
	b, err := os.ReadFile("testdata/conflict/merge.md")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Parse("merge.md", b)
	var ce *ErrConflicted
	if !errors.As(err, &ce) {
		t.Fatalf("want ErrConflicted, got %v", err)
	}
	if ce.Line != 3 {
		t.Errorf("conflict line = %d, want 3", ce.Line)
	}
}

// TestFencesAreNotTasks is a correctness requirement, not a nicety: a task-like
// line inside a code fence must never become sortable.
func TestFencesAreNotTasks(t *testing.T) {
	b, err := os.ReadFile("testdata/corpus/fences.md")
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse("fences.md", b)
	if err != nil {
		t.Fatal(err)
	}
	for i, ln := range f.Lines {
		if ln.Kind == LineTask && (strings.Contains(ln.Raw, "alphabetically") ||
			strings.Contains(ln.Raw, "nested fence") || strings.Contains(ln.Raw, "not a task")) {
			t.Errorf("line %d classified as a task inside a fence: %q", i+1, ln.Raw)
		}
	}
	want := []string{"Document the rollback", "Test the drain script", "Ship it", "Last one"}
	var got []string
	for _, tk := range f.Tasks() {
		got = append(got, tk.Desc)
	}
	if len(got) != len(want) {
		t.Fatalf("tasks = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("task %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func parseOne(t *testing.T, line string) *Task {
	t.Helper()
	f, err := Parse("t.md", []byte(line+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	tasks := f.Tasks()
	if len(tasks) != 1 {
		t.Fatalf("parsed %d tasks from %q, want 1", len(tasks), line)
	}
	return tasks[0]
}

func TestParseFields(t *testing.T) {
	d := func(s string) *Date {
		v, ok := ParseDate(s)
		if !ok {
			t.Fatalf("bad test date %q", s)
		}
		return &v
	}

	cases := []struct {
		name  string
		line  string
		check func(*testing.T, *Task)
	}{
		{"open", "- [ ] a", func(t *testing.T, k *Task) { eq(t, "state", k.State, Open) }},
		{"in progress", "- [/] a", func(t *testing.T, k *Task) { eq(t, "state", k.State, InProgress) }},
		{"done", "- [x] a", func(t *testing.T, k *Task) { eq(t, "state", k.State, Done) }},
		{"done upper", "- [X] a", func(t *testing.T, k *Task) { eq(t, "state", k.State, Done) }},
		{"cancelled", "- [-] a", func(t *testing.T, k *Task) { eq(t, "state", k.State, Cancelled) }},

		{"highest", "- [ ] a 🔺", func(t *testing.T, k *Task) { eq(t, "prio", k.Priority, Highest) }},
		{"high", "- [ ] a ⏫", func(t *testing.T, k *Task) { eq(t, "prio", k.Priority, High) }},
		{"medium", "- [ ] a 🔼", func(t *testing.T, k *Task) { eq(t, "prio", k.Priority, Medium) }},
		{"none", "- [ ] a", func(t *testing.T, k *Task) { eq(t, "prio", k.Priority, Normal) }},
		{"low", "- [ ] a 🔽", func(t *testing.T, k *Task) { eq(t, "prio", k.Priority, Low) }},
		{"lowest", "- [ ] a ⏬", func(t *testing.T, k *Task) { eq(t, "prio", k.Priority, Lowest) }},

		{"due", "- [ ] a 📅 2026-08-09", func(t *testing.T, k *Task) { eq(t, "due", *k.Due, *d("2026-08-09")) }},
		{"created", "- [ ] a ➕ 2026-08-09", func(t *testing.T, k *Task) { eq(t, "created", *k.Created, *d("2026-08-09")) }},
		{"start", "- [ ] a 🛫 2026-08-09", func(t *testing.T, k *Task) { eq(t, "start", *k.Start, *d("2026-08-09")) }},
		{"scheduled", "- [ ] a ⏳ 2026-08-09", func(t *testing.T, k *Task) { eq(t, "scheduled", *k.Scheduled, *d("2026-08-09")) }},
		{"completed", "- [x] a ✅ 2026-08-09", func(t *testing.T, k *Task) { eq(t, "completed", *k.Completed, *d("2026-08-09")) }},
		{"cancelled date", "- [-] a ❌ 2026-08-09", func(t *testing.T, k *Task) { eq(t, "cancelledOn", *k.CancelledOn, *d("2026-08-09")) }},

		{"recurrence", "- [ ] a 🔁 every week on Monday", func(t *testing.T, k *Task) {
			eq(t, "recurrence", k.Recurrence, "every week on Monday")
		}},
		{"id", "- [ ] a 🆔 abc123", func(t *testing.T, k *Task) { eq(t, "id", k.ID, "abc123") }},
		{"blocked by", "- [ ] a ⛔ x1,y2", func(t *testing.T, k *Task) {
			eq(t, "blockedBy", strings.Join(k.BlockedBy, "|"), "x1|y2")
		}},
		{"on completion", "- [ ] a 🏁 delete", func(t *testing.T, k *Task) { eq(t, "onCompletion", k.OnCompletion, "delete") }},

		{"malformed date stays in desc", "- [ ] a 📅 2026-13-45", func(t *testing.T, k *Task) {
			if k.Due != nil {
				t.Errorf("due = %v, want nil", k.Due)
			}
			eq(t, "desc", k.Desc, "a 📅 2026-13-45")
		}},
		{"normalising date is rejected", "- [ ] a 📅 2026-02-31", func(t *testing.T, k *Task) {
			if k.Due != nil {
				t.Errorf("due = %v, want nil (2026-02-31 is not a real date)", k.Due)
			}
		}},
		{"bad on-completion stays in desc", "- [ ] a 🏁 maybe", func(t *testing.T, k *Task) {
			eq(t, "onCompletion", k.OnCompletion, "")
			eq(t, "desc", k.Desc, "a 🏁 maybe")
		}},
		{"duplicate marker keeps the first", "- [ ] a ⏫ ⏫", func(t *testing.T, k *Task) {
			eq(t, "prio", k.Priority, High)
			eq(t, "desc", k.Desc, "a ⏫")
		}},
		{"marker glued to a word is text", "- [ ] a⏫b", func(t *testing.T, k *Task) {
			eq(t, "prio", k.Priority, Normal)
			eq(t, "desc", k.Desc, "a⏫b")
		}},
		{"text after a field rejoins the description", "- [ ] a 📅 2026-08-01 and more", func(t *testing.T, k *Task) {
			eq(t, "desc", k.Desc, "a and more")
			eq(t, "due", *k.Due, *d("2026-08-01"))
		}},

		// The two spec footguns.
		{"nbsp separators", "- [ ] a\u00a0⏫\u00a0📅\u00a02026-08-09", func(t *testing.T, k *Task) {
			eq(t, "prio", k.Priority, High)
			eq(t, "due", *k.Due, *d("2026-08-09"))
			eq(t, "desc", k.Desc, "a")
		}},
		{"variation selector", "- [ ] a ✅\uFE0F 2026-08-01 ⏳\uFE0F 2026-08-02", func(t *testing.T, k *Task) {
			eq(t, "completed", *k.Completed, *d("2026-08-01"))
			eq(t, "scheduled", *k.Scheduled, *d("2026-08-02"))
			eq(t, "desc", k.Desc, "a")
		}},

		{"bullets", "* [ ] a", func(t *testing.T, k *Task) { eq(t, "bullet", k.Bullet, "*") }},
		{"plus bullet", "+ [ ] a", func(t *testing.T, k *Task) { eq(t, "bullet", k.Bullet, "+") }},
		{"indent preserved", "\t  - [ ] a", func(t *testing.T, k *Task) { eq(t, "indent", k.Indent, "\t  ") }},
		{"empty description", "- [ ]", func(t *testing.T, k *Task) { eq(t, "desc", k.Desc, "") }},

		{"tags", "- [ ] a #k8s #infra/aws not#atag", func(t *testing.T, k *Task) {
			eq(t, "tags", strings.Join(k.Tags, "|"), "k8s|infra/aws")
			eq(t, "desc", k.Desc, "a #k8s #infra/aws not#atag")
		}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { c.check(t, parseOne(t, c.line)) })
	}
}

func eq[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func TestCRLFRoundTripAndParse(t *testing.T) {
	src := "# h\r\n\r\n- [ ] alpha ⏫\r\n- [x] beta ✅ 2026-08-01\r\n"
	f, err := Parse("crlf.md", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if string(f.Bytes()) != src {
		t.Fatalf("round trip = %q", f.Bytes())
	}
	tasks := f.Tasks()
	eq(t, "desc", tasks[0].Desc, "alpha")
	eq(t, "prio", tasks[0].Priority, High)
	eq(t, "eol", f.EOL(), "\r\n")
}

func TestCanonicalOrder(t *testing.T) {
	k := parseOne(t, "- [ ] ship it #infra")
	k.dirty = true
	k.Priority = High
	k.Recurrence = "every week"
	d := func(s string) *Date { v, _ := ParseDate(s); return &v }
	k.Start, k.Scheduled, k.Due = d("2026-08-10"), d("2026-08-11"), d("2026-08-12")
	k.ID, k.BlockedBy, k.OnCompletion = "a1b2", []string{"x9", "y8"}, "keep"
	k.Created, k.Completed = d("2026-08-01"), d("2026-08-20")
	k.State = Done

	want := "- [x] ship it #infra ⏫ 🔁 every week 🛫 2026-08-10 ⏳ 2026-08-11 📅 2026-08-12 " +
		"🆔 a1b2 ⛔ x9,y8 🏁 keep ➕ 2026-08-01 ✅ 2026-08-20"
	if got := k.Canonical(); got != want {
		t.Errorf("canonical =\n%q\nwant\n%q", got, want)
	}
}

// TestMutationIsLocalised is the property that keeps git diffs the size of the
// actual change.
func TestMutationIsLocalised(t *testing.T) {
	src, err := os.ReadFile("testdata/corpus/fields.md")
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse("fields.md", src)
	if err != nil {
		t.Fatal(err)
	}
	// "Hand-written order" has its fields in a non-canonical order; completing
	// the task next to it must not touch it.
	var target *Task
	for _, k := range f.Tasks() {
		if strings.HasPrefix(k.Desc, "All of them") {
			target = k
		}
	}
	if target == nil {
		t.Fatal("target task not found")
	}
	target.SetState(Done)

	before := strings.Split(string(src), "\n")
	after := strings.Split(string(f.Bytes()), "\n")
	if len(before) != len(after) {
		t.Fatalf("line count changed: %d -> %d", len(before), len(after))
	}
	changed := 0
	for i := range before {
		if before[i] != after[i] {
			changed++
		}
	}
	if changed != 1 {
		t.Errorf("%d lines changed, want exactly 1", changed)
	}
	if !strings.Contains(after[4], "- [x] All of them") {
		t.Errorf("mutated line = %q", after[4])
	}
}

func TestRewritableRefusesAmbiguousLines(t *testing.T) {
	if err := parseOne(t, "- [ ] clean line ⏫ 📅 2026-08-01").Rewritable(); err != nil {
		t.Errorf("clean line refused: %v", err)
	}
	for _, line := range []string{
		"- [ ] malformed 📅 2026-13-45",
		"- [ ] duplicate ⏫ ⏫",
		"- [ ] bad completion 🏁 maybe",
	} {
		if err := parseOne(t, line).Rewritable(); err == nil {
			t.Errorf("%q: want a refusal, got nil", line)
		}
	}
}

func TestMarkerParsing(t *testing.T) {
	f, err := Parse("m.md", []byte("<!-- tuido: sort=created capture=backlog x=y -->\n- [ ] a\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !f.Marker.Present {
		t.Fatal("marker not found")
	}
	eq(t, "sort", f.Marker.Sort, "created")
	eq(t, "capture", f.Marker.Capture, "backlog")
	eq(t, "unknown key preserved", f.Marker.Keys["x"], "y")

	// Only the first non-blank line counts, so a comment further down is prose.
	f2, err := Parse("m.md", []byte("# h\n<!-- tuido: sort=none -->\n"))
	if err != nil {
		t.Fatal(err)
	}
	if f2.Marker.Present {
		t.Error("marker recognised below the first body line")
	}
}

func TestBlocks(t *testing.T) {
	b, err := os.ReadFile("testdata/corpus/nested.md")
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse("nested.md", b)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(f.Blocks))
	}
	blk := f.Blocks[0]
	if len(blk.Items) != 3 {
		t.Fatalf("top-level items = %d, want 3", len(blk.Items))
	}
	eq(t, "section", blk.Section, "nested")
	// Parent one owns its continuation line, both children and both grandchildren.
	if n := blk.Items[0].End - blk.Items[0].Start + 1; n != 6 {
		t.Errorf("item 0 spans %d lines, want 6", n)
	}
	eq(t, "item 1 desc", blk.Items[1].Task.Desc, "Parent two")
	if n := blk.Items[1].End - blk.Items[1].Start + 1; n != 2 {
		t.Errorf("item 1 spans %d lines, want 2 (task + tab continuation)", n)
	}
}

func TestHeadingPathsPreserveHierarchyAndRepeatedSections(t *testing.T) {
	src := "- [ ] ungrouped\n" +
		"# Backend\n- [ ] parent\n" +
		"### Bugs\n- [ ] nested\n" +
		"# Backend\n- [ ] repeated\n"
	f, err := Parse("work/oncall.md", []byte(src))
	if err != nil {
		t.Fatal(err)
	}

	paths := f.HeadingPaths()
	if _, ok := paths[1]; ok {
		t.Fatal("headingless task unexpectedly has a heading path")
	}
	if got := paths[3]; len(got) != 1 || got[0].Text != "Backend" || got[0].Line != 2 {
		t.Errorf("parent path = %#v", got)
	}
	if got := paths[5]; len(got) != 2 || got[0].Text != "Backend" || got[1].Text != "Bugs" {
		t.Errorf("nested path = %#v", got)
	}
	if got := paths[7]; len(got) != 1 || got[0].Text != "Backend" || got[0].Line != 6 {
		t.Errorf("repeated path = %#v", got)
	}
}

func TestHeadingPathsOmitEmptyHeadings(t *testing.T) {
	f, err := Parse("work/oncall.md", []byte("#\n- [ ] parent\n## Child\n- [ ] nested\n"))
	if err != nil {
		t.Fatal(err)
	}
	paths := f.HeadingPaths()
	if got := paths[2]; len(got) != 0 {
		t.Errorf("task under empty heading has path %#v", got)
	}
	if got := paths[4]; len(got) != 1 || got[0].Text != "Child" {
		t.Errorf("nested task path = %#v", got)
	}
}

// A blank line ends a block: loose lists are never reordered. This is the
// documented consequence of the fence rule.
func TestBlankLineEndsBlock(t *testing.T) {
	b, err := os.ReadFile("testdata/corpus/loose.md")
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse("loose.md", b)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Blocks) != 4 {
		t.Fatalf("blocks = %d, want 4 (three singletons plus one pair)", len(f.Blocks))
	}
	if len(f.Blocks[3].Items) != 2 {
		t.Errorf("last block items = %d, want 2", len(f.Blocks[3].Items))
	}
}

func TestReorderKeepsFinalNewlineProperty(t *testing.T) {
	src := "- [ ] a\n- [ ] b\n- [ ] c"
	f, err := Parse("x.md", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	f.ReorderBlock(f.Blocks[0], []int{2, 1, 0})
	want := "- [ ] c\n- [ ] b\n- [ ] a"
	if got := string(f.Bytes()); got != want {
		t.Errorf("reordered = %q, want %q", got, want)
	}
}

func TestReorderCarriesChildren(t *testing.T) {
	src := "- [ ] parent one\n  - [ ] child\n  cont\n- [ ] parent two\n"
	f, err := Parse("x.md", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if moved := f.ReorderBlock(f.Blocks[0], []int{1, 0}); moved != 2 {
		t.Errorf("moved = %d, want 2", moved)
	}
	want := "- [ ] parent two\n- [ ] parent one\n  - [ ] child\n  cont\n"
	if got := string(f.Bytes()); got != want {
		t.Errorf("reordered =\n%q\nwant\n%q", got, want)
	}
}
