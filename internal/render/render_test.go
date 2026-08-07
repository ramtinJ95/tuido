package render

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ramtinJ95/tuido/internal/task"
)

func parseTasks(t *testing.T, src string) []*task.Task {
	t.Helper()
	f, err := task.Parse("work/oncall.md", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return f.Tasks()
}

func newTestRenderer(buf *bytes.Buffer) *Renderer {
	return &Renderer{w: buf, Color: false, Width: 60, Today: task.Date{Year: 2026, Month: 8, Day: 8}}
}

func TestPlainOutputHasNoEscapes(t *testing.T) {
	var buf bytes.Buffer
	r := newTestRenderer(&buf)
	r.Groups([]Group{{
		Ref:   "work/oncall",
		Tasks: parseTasks(t, "- [ ] fix the drain ⏫ 📅 2026-08-09 ➕ 2026-08-06\n"),
	}}, false)

	out := buf.String()
	if strings.Contains(out, "\x1b") {
		t.Errorf("escape sequences with Color=false: %q", out)
	}
	for _, want := range []string{"work/oncall", "▲", "fix the drain", "08-09", "2d"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestColourUsesPaletteIndicesOnly(t *testing.T) {
	var buf bytes.Buffer
	r := newTestRenderer(&buf)
	r.Color = true
	r.Groups([]Group{{
		Ref: "work/oncall",
		Tasks: parseTasks(t, "- [ ] highest 🔺\n- [ ] overdue 📅 2026-08-01\n"+
			"- [x] finished ✅ 2026-08-07\n- [/] running\n"),
	}}, false)

	out := buf.String()
	// A hex colour here would pin tuido's palette against the terminal theme.
	if strings.Contains(out, "38;2;") || strings.Contains(out, "48;2;") {
		t.Errorf("truecolor escape in output: %q", out)
	}
	for _, want := range []string{"\x1b[34m", "\x1b[1;31m", "\x1b[31m", "\x1b[32m", "\x1b[36m"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected SGR %q in output:\n%q", want, out)
		}
	}
}

func TestHiddenFooter(t *testing.T) {
	var buf bytes.Buffer
	r := newTestRenderer(&buf)
	r.Groups([]Group{{
		Ref:    "work/oncall",
		Tasks:  parseTasks(t, "- [ ] visible\n"),
		Hidden: map[string]int{"done": 2, "blocked": 1},
	}}, true)

	out := buf.String()
	if !strings.Contains(out, "3 hidden: 1 blocked, 2 done") {
		t.Errorf("footer = %q", out)
	}
	if !strings.Contains(out, "tuido ls --all") {
		t.Errorf("hint missing:\n%s", out)
	}
}

func TestEmptyRendersSomething(t *testing.T) {
	var buf bytes.Buffer
	newTestRenderer(&buf).Groups(nil, false)
	if !strings.Contains(buf.String(), "nothing to do") {
		t.Errorf("empty output = %q", buf.String())
	}
}

func TestLongDescriptionIsTruncated(t *testing.T) {
	var buf bytes.Buffer
	r := newTestRenderer(&buf)
	long := strings.Repeat("very long description ", 10)
	r.Groups([]Group{{Ref: "w/l", Tasks: parseTasks(t, "- [ ] "+long+"\n")}}, false)

	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if width(line) > r.Width {
			t.Errorf("line is %d columns, terminal is %d: %q", width(line), r.Width, line)
		}
	}
	if !strings.Contains(buf.String(), "…") {
		t.Error("no ellipsis on a truncated description")
	}
}

func TestGlyphSelection(t *testing.T) {
	var buf bytes.Buffer
	r := newTestRenderer(&buf)
	cases := []struct {
		line  string
		glyph string
	}{
		{"- [ ] a 🔺", gHighest},
		{"- [ ] a ⏫", gHigh},
		{"- [ ] a 🔼", gMedium},
		{"- [ ] a", gNormal},
		{"- [ ] a 🔽", gLow},
		{"- [ ] a ⏬", gLowest},
		{"- [/] a", gProgress},
		{"- [x] a", gDone},
		{"- [-] a", gCancelled},
		{"- [ ] a ⛔ xyz", gBlocked},
	}
	for _, c := range cases {
		got, _ := r.glyph(parseTasks(t, c.line+"\n")[0])
		if got != c.glyph {
			t.Errorf("%s → %q, want %q", c.line, got, c.glyph)
		}
	}
}

// Every glyph tuido emits must be single-width, or the columns drift.
func TestGlyphsAreSingleWidth(t *testing.T) {
	for _, g := range []string{gHighest, gHigh, gMedium, gNormal, gLow, gLowest,
		gProgress, gDone, gCancelled, gBlocked} {
		if w := width(g); w != 1 {
			t.Errorf("glyph %q is %d columns wide", g, w)
		}
	}
}
