package task

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// FuzzHeadingPaths exercises real temporary Markdown documents and checks the
// production hierarchy against an independent stack-based model. It covers
// arbitrary heading levels, repeated and empty headings, fences, malformed
// Markdown, line endings, and tasks before or between sections.
func FuzzHeadingPaths(f *testing.F) {
	seeds := [][]byte{
		[]byte("- [ ] headingless\n"),
		[]byte("# Backend\n- [ ] parent\n## Bugs\n- [/] nested\n"),
		[]byte("### Skipped levels\n- [ ] task\n###### Deep\n- [ ] deeper\n"),
		[]byte("# Same\n- [ ] first\n# Same\n- [ ] second\n"),
		[]byte("#\n- [ ] empty parent\n### Child\n- [ ] visible child\n"),
		[]byte("# Visible\n- [ ] open\n# Hidden only\n- [x] done\n# Empty\nprose\n"),
		[]byte("```md\n# Not a heading\n- [ ] not a task\n```\n# Real\n- [ ] task\n"),
		[]byte("# Windows\r\n- [ ] task\r\n## Child\r\n- [ ] nested\r\n"),
		[]byte("####nospace\n- [ ] headingless\n## Valid ##\n- [ ] task\n"),
		[]byte("# 日本語\n- [ ] unicode\n## café 🚀\n- [ ] nested\n"),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, contents []byte) {
		if len(contents) > 64<<10 {
			t.Skip()
		}
		path := filepath.Join(t.TempDir(), "work", "oncall.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, contents, 0o644); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		file, err := Parse(path, body)
		if err != nil {
			return // conflict markers are a deliberate parse refusal
		}

		got := file.HeadingPaths()
		want := referenceHeadingPaths(file)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("heading paths differ\ninput: %q\n got: %#v\nwant: %#v", contents, got, want)
		}
		if again := file.HeadingPaths(); !reflect.DeepEqual(again, got) {
			t.Fatalf("heading paths are not deterministic\nfirst: %#v\nagain: %#v", got, again)
		}
	})
}

type referenceHeading struct {
	heading Heading
	level   int
}

func referenceHeadingPaths(file *File) map[int][]Heading {
	paths := make(map[int][]Heading)
	var active []referenceHeading
	for i, line := range file.Lines {
		switch line.Kind {
		case LineHeading:
			level := referenceHeadingLevel(line.Raw)
			for len(active) > 0 && active[len(active)-1].level >= level {
				active = active[:len(active)-1]
			}
			text := strings.TrimSpace(strings.TrimLeft(line.Raw, "# "))
			active = append(active, referenceHeading{
				heading: Heading{Text: text, Line: i + 1},
				level:   level,
			})
		case LineTask:
			for _, h := range active {
				if h.heading.Text != "" {
					paths[line.Task.Line] = append(paths[line.Task.Line], h.heading)
				}
			}
			if len(paths[line.Task.Line]) == 0 {
				delete(paths, line.Task.Line)
			}
		}
	}
	return paths
}

func referenceHeadingLevel(raw string) int {
	level := 0
	for level < len(raw) && raw[level] == '#' {
		level++
	}
	return level
}
