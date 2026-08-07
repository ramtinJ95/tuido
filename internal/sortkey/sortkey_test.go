package sortkey

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ramtinJ95/tuido/internal/task"
)

var update = flag.Bool("update", false, "rewrite golden files")

// TestGolden sorts every testdata/sort/<case>.in.md and compares it with the
// matching .golden.md, then sorts the golden file again to prove idempotence.
func TestGolden(t *testing.T) {
	ins, err := filepath.Glob("testdata/sort/*.in.md")
	if err != nil || len(ins) == 0 {
		t.Fatalf("no golden inputs: %v", err)
	}
	for _, in := range ins {
		name := strings.TrimSuffix(filepath.Base(in), ".in.md")
		t.Run(name, func(t *testing.T) {
			mode := ByPrio
			switch {
			case strings.HasPrefix(name, "by-due"):
				mode = ByDue
			case strings.HasPrefix(name, "by-created"):
				mode = ByCreated
			}

			got := sortBytes(t, in, mode)
			golden := strings.TrimSuffix(in, ".in.md") + ".golden.md"
			if *update {
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(want) {
				t.Fatalf("sorted output differs\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}

			// Idempotence: sorting a sorted file must change nothing.
			again := sortBytes(t, golden, mode)
			if string(again) != string(want) {
				t.Fatalf("sort is not idempotent\n--- second pass ---\n%s", again)
			}
		})
	}
}

func sortBytes(t *testing.T, path string, mode Mode) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := task.Parse(path, b)
	if err != nil {
		t.Fatal(err)
	}
	m, err := ModeFor(f, mode)
	if err != nil {
		t.Fatal(err)
	}
	File(f, m, openIDs(f))
	return f.Bytes()
}

func openIDs(f *task.File) map[string]bool {
	ids := map[string]bool{}
	for _, k := range f.Tasks() {
		if k.ID != "" && !k.State.Closed() {
			ids[k.ID] = true
		}
	}
	return ids
}

// A sorted file must be byte-identical to the original apart from the order of
// task lines: sorting never rewrites a task.
func TestSortDoesNotRewriteTasks(t *testing.T) {
	b, err := os.ReadFile("testdata/sort/mixed.in.md")
	if err != nil {
		t.Fatal(err)
	}
	f, err := task.Parse("mixed.md", b)
	if err != nil {
		t.Fatal(err)
	}
	File(f, ByPrio, nil)
	for _, k := range f.Tasks() {
		if k.Dirty() {
			t.Errorf("sort marked a task dirty: %q", k.Raw())
		}
	}
	if len(strings.Split(string(b), "\n")) != len(strings.Split(string(f.Bytes()), "\n")) {
		t.Error("line count changed")
	}
}

// The fence rule, asserted directly: nothing outside a task block may move.
func TestFencesAreImmovable(t *testing.T) {
	b, err := os.ReadFile("testdata/sort/fences.in.md")
	if err != nil {
		t.Fatal(err)
	}
	f, err := task.Parse("fences.md", b)
	if err != nil {
		t.Fatal(err)
	}
	before := map[int]string{}
	for i, ln := range f.Lines {
		if ln.Kind != task.LineTask && ln.Kind != task.LineTaskCont {
			before[i] = ln.Raw
		}
	}
	File(f, ByPrio, nil)
	for i, raw := range before {
		if f.Lines[i].Raw != raw {
			t.Errorf("line %d moved: %q -> %q", i+1, raw, f.Lines[i].Raw)
		}
	}
}

func TestSortNoneSkipsFile(t *testing.T) {
	src := "<!-- tuido: sort=none -->\n- [ ] z ⏬\n- [ ] a 🔺\n"
	f, err := task.Parse("x.md", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	m, err := ModeFor(f, "")
	if err != nil {
		t.Fatal(err)
	}
	if m != None {
		t.Fatalf("mode = %q, want none", m)
	}
	File(f, m, nil)
	if string(f.Bytes()) != src {
		t.Errorf("sort=none file was reordered:\n%s", f.Bytes())
	}

	// An explicit --by overrides the marker.
	m2, _ := ModeFor(f, ByPrio)
	File(f, m2, nil)
	if string(f.Bytes()) == src {
		t.Error("explicit --by did not override sort=none")
	}
}

func TestBlockedNeverSortsToTop(t *testing.T) {
	src := "- [ ] blocked but urgent 🔺 ⛔ abc\n- [ ] plain 🔽\n- [ ] blocker 🆔 abc\n"
	f, err := task.Parse("x.md", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	File(f, ByPrio, openIDs(f))
	first := f.Tasks()[0]
	if strings.Contains(first.Desc, "blocked but urgent") {
		t.Errorf("blocked task sorted to the top:\n%s", f.Bytes())
	}
}
