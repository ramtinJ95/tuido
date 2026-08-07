package task

import (
	"os"
	"testing"
)

// FuzzParse is the single highest-value test in the project: a parser bug does
// not crash, it quietly rewrites a line and git dutifully commits it.
//
// Two properties are asserted:
//
//  1. Round-trip identity — parsing and writing back never changes a byte.
//  2. Canonical stability — for a task tuido is willing to rewrite, emitting it
//     canonically and reparsing yields the same canonical form. Without this,
//     a mutation could oscillate between two spellings on every run.
func FuzzParse(f *testing.F) {
	for _, p := range mustGlob(f, "testdata/corpus/*.md") {
		b, err := os.ReadFile(p)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte("- [ ] a ⏫ 📅 2026-08-09 ➕ 2026-08-01\n"))
	f.Add([]byte("```\n- [ ] in a fence\n"))
	f.Add([]byte("- [ ]"))
	f.Add([]byte("\r\n- [x] \r\n"))

	f.Fuzz(func(t *testing.T, b []byte) {
		file, err := Parse("fuzz.md", b)
		if err != nil {
			return // conflicted input is a legitimate refusal
		}
		if got := file.Bytes(); string(got) != string(b) {
			t.Fatalf("round trip changed bytes\nin:  %q\nout: %q", b, got)
		}
		for _, k := range file.Tasks() {
			if k.Rewritable() != nil {
				continue
			}
			c1 := k.Canonical()
			again, err := Parse("fuzz.md", []byte(c1+"\n"))
			if err != nil {
				t.Fatalf("canonical output does not reparse: %q: %v", c1, err)
			}
			tasks := again.Tasks()
			if len(tasks) != 1 {
				t.Fatalf("canonical output %q parsed as %d tasks", c1, len(tasks))
			}
			if c2 := tasks[0].Canonical(); c2 != c1 {
				t.Fatalf("canonical form is unstable\nfrom: %q\nc1:   %q\nc2:   %q", k.raw, c1, c2)
			}
		}
	})
}

func mustGlob(f *testing.F, pat string) []string {
	f.Helper()
	paths, err := globSorted(pat)
	if err != nil || len(paths) == 0 {
		f.Fatalf("glob %s: %v", pat, err)
	}
	return paths
}
