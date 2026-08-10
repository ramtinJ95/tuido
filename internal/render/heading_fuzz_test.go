package render

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ramtinJ95/tuido/internal/task"
)

// FuzzGroupsWithHeadings renders arbitrary subsets of tasks from temporary
// Markdown documents. The line-count invariant catches duplicate, missing, or
// empty heading output; transition checks ensure every required nested heading
// appears in source order; rendering the same group twice must be identical.
func FuzzGroupsWithHeadings(f *testing.F) {
	seeds := [][]byte{
		[]byte("- [ ] headingless\n"),
		[]byte("# Backend\n- [ ] parent\n## Bugs\n- [/] nested\n"),
		[]byte("### Skipped\n- [ ] task\n###### Deep\n- [ ] deeper\n"),
		[]byte("# Same\n- [ ] first\n# Same\n- [ ] second\n"),
		[]byte("#\n- [ ] empty\n## Child\n- [ ] nested\n"),
		[]byte("```\n# fake\n- [ ] fake\n```\n# real\n- [ ] real\n"),
		[]byte("# Alpha\r\n- [ ] one\r\n## Beta\r\n- [x] two\r\n"),
	}
	for i, seed := range seeds {
		f.Add(seed, uint8(1<<uint(i%8)), uint8(40+i*17))
		f.Add(seed, uint8(0xff), uint8(120))
	}

	f.Fuzz(func(t *testing.T, contents []byte, selection, fuzzWidth uint8) {
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
		file, err := task.Parse(path, body)
		if err != nil {
			return // conflict markers are a deliberate parse refusal
		}

		var visible []*task.Task
		for i, candidate := range file.Tasks() {
			if selection&(1<<uint(i%8)) != 0 {
				visible = append(visible, candidate)
			}
		}
		group := Group{
			Ref:      "work/oncall",
			Tasks:    visible,
			Headings: file.HeadingPaths(),
		}
		width := 20 + int(fuzzWidth)%181
		out := fuzzRender(group, width)
		if again := fuzzRender(group, width); out != again {
			t.Fatalf("render is not deterministic\nfirst: %q\nagain: %q", out, again)
		}

		headingLines := 0
		cursor := 0
		var previous []task.Heading
		for _, visibleTask := range visible {
			path := group.Headings[visibleTask.Line]
			common := commonHeadings(previous, path)
			for depth := common; depth < len(path); depth++ {
				headingLines++
				needle := strings.Repeat(" ", 2*(depth+1)) + path[depth].Text + "\n"
				i := strings.Index(out[cursor:], needle)
				if i < 0 {
					t.Fatalf("missing heading transition %q after byte %d\ninput: %q\noutput: %q", needle, cursor, contents, out)
				}
				cursor += i + len(needle)
			}
			previous = path
		}

		wantLines := 1 // list header, or the single "nothing to do" line
		if len(visible) > 0 {
			wantLines += len(visible) + headingLines
		}
		if got := strings.Count(out, "\n"); got != wantLines {
			t.Fatalf("rendered %d lines, want %d\ninput: %q\noutput: %q", got, wantLines, contents, out)
		}
	})
}

func fuzzRender(group Group, width int) string {
	var out bytes.Buffer
	r := &Renderer{
		w:     &out,
		Color: false,
		Width: width,
		Today: task.Date{Year: 2026, Month: 8, Day: 10},
	}
	r.Groups([]Group{group}, false)
	return out.String()
}
