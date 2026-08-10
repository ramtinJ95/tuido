// Package render writes task lists to a terminal using ANSI palette indices
// 0–15 only, never a hex value, so the colours are whatever the terminal theme
// says they are.
package render

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"golang.org/x/term"

	"github.com/ramtinJ95/tuido/internal/task"
)

// SGR codes. Palette indices only — a truecolor value here would pin tuido's
// idea of "red" against the user's theme, which is exactly what we are avoiding.
const (
	sgrReset     = "0"
	sgrHighest   = "1;31" // bold color1
	sgrHigh      = "31"   // color1
	sgrMedium    = "33"   // color3
	sgrLow       = "90"   // color8
	sgrProgress  = "36"   // color6
	sgrDone      = "32"   // color2
	sgrCancelled = "90"
	sgrBlocked   = "90"
	sgrHeader    = "34" // color4
	sgrMeta      = "90"
	sgrOverdue   = "31"
)

// Glyphs are all single-width. No emoji reaches the output, which is what keeps
// the columns aligned regardless of the terminal's emoji metrics.
const (
	gHighest   = "▲"
	gHigh      = "▴"
	gMedium    = "△"
	gNormal    = "•"
	gLow       = "▽"
	gLowest    = "▿"
	gProgress  = "◐"
	gDone      = "✓"
	gCancelled = "✗"
	gBlocked   = "⊘"
)

// Renderer writes to w, styling only when the terminal will make sense of it.
type Renderer struct {
	w     io.Writer
	Color bool
	Width int
	Today task.Date
}

// New builds a renderer for w. Styling is disabled — absent, not empty escape
// sequences — when NO_COLOR is set, TERM is dumb, or the output is not a TTY.
func New(w io.Writer) *Renderer {
	r := &Renderer{w: w, Width: 80, Today: task.Today()}

	fd, isFile := w.(*os.File)
	tty := isFile && term.IsTerminal(int(fd.Fd()))
	_, noColor := os.LookupEnv("NO_COLOR")
	r.Color = tty && !noColor && os.Getenv("TERM") != "dumb"

	if tty {
		if cols, _, err := term.GetSize(int(fd.Fd())); err == nil && cols > 20 {
			r.Width = cols
		}
	}
	return r
}

func (r *Renderer) style(code, s string) string {
	if !r.Color || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[" + sgrReset + "m"
}

// Group is one list's worth of output.
type Group struct {
	Ref      string
	Tasks    []*task.Task
	Headings map[int][]task.Heading // task line -> active heading hierarchy
	Hidden   map[string]int         // reason -> count
}

// Warn prints the one-line sync warning that sits above normal output.
func (r *Renderer) Warn(msg string) {
	fmt.Fprintln(r.w, r.style(sgrMeta, "⚠ "+msg))
}

// Errorf prints a user-facing error.
func Errorf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "tuido: "+format+"\n", args...)
}

// Groups renders every group, with a footer per group counting what was hidden
// and why.
func (r *Renderer) Groups(groups []Group, showAllHint bool) {
	first := true
	for _, g := range groups {
		if len(g.Tasks) == 0 && total(g.Hidden) == 0 {
			continue
		}
		if !first {
			fmt.Fprintln(r.w)
		}
		first = false

		fmt.Fprintln(r.w, r.style(sgrHeader, g.Ref))
		var previous []task.Heading
		for _, t := range g.Tasks {
			path := g.Headings[t.Line]
			common := commonHeadings(previous, path)
			for depth := common; depth < len(path); depth++ {
				indent := strings.Repeat(" ", 2*(depth+1))
				fmt.Fprintln(r.w, indent+r.style(sgrHeader, path[depth].Text))
			}
			lead := 2
			if len(path) > 0 {
				lead = 2 * (len(path) + 1)
			}
			fmt.Fprintln(r.w, r.lineAt(t, lead))
			previous = path
		}
		if n := total(g.Hidden); n > 0 {
			fmt.Fprintln(r.w)
			fmt.Fprintf(r.w, "  %s\n", r.style(sgrMeta, fmt.Sprintf("%d hidden: %s", n, reasons(g.Hidden))))
			if showAllHint {
				fmt.Fprintf(r.w, "  %s\n", r.style(sgrMeta, "→ tuido ls --all"))
			}
		}
	}
	if first {
		fmt.Fprintln(r.w, r.style(sgrMeta, "nothing to do"))
	}
}

func commonHeadings(a, b []task.Heading) int {
	n := min(len(a), len(b))
	for i := 0; i < n; i++ {
		if a[i].Line != b[i].Line {
			return i
		}
	}
	return n
}

// line lays out one task: glyph, description, due date, age.
func (r *Renderer) line(t *task.Task) string {
	return r.lineAt(t, 2)
}

func (r *Renderer) lineAt(t *task.Task, lead int) string {
	glyph, code := r.glyph(t)

	due, dueCode := "", sgrMeta
	if t.Due != nil {
		due = fmt.Sprintf("%02d-%02d", t.Due.Month, t.Due.Day)
		if t.Due.Compare(r.Today) < 0 {
			dueCode = sgrOverdue
		}
	}
	age := ""
	if t.Created != nil {
		if d := t.Created.DaysSince(r.Today); d > 0 {
			age = fmt.Sprintf("%dd", d)
		}
	}

	const (
		gap    = 2
		dueCol = 5
		ageCol = 4
	)
	descWidth := r.Width - lead - width(glyph) - gap - gap - dueCol - gap - ageCol
	if descWidth < 10 {
		descWidth = 10
	}
	desc := truncate(t.Desc, descWidth)

	var b strings.Builder
	b.WriteString(strings.Repeat(" ", lead))
	b.WriteString(r.style(code, glyph))
	b.WriteString(strings.Repeat(" ", gap))
	b.WriteString(desc)
	b.WriteString(strings.Repeat(" ", max(1, descWidth-width(desc)+gap)))
	b.WriteString(pad(r.style(dueCode, due), due, dueCol))
	b.WriteString(strings.Repeat(" ", gap))
	b.WriteString(padLeft(r.style(sgrMeta, age), age, ageCol))
	return strings.TrimRight(b.String(), " ")
}

func (r *Renderer) glyph(t *task.Task) (string, string) {
	switch t.State {
	case task.Done:
		return gDone, sgrDone
	case task.Cancelled:
		return gCancelled, sgrCancelled
	case task.InProgress:
		return gProgress, sgrProgress
	}
	if len(t.BlockedBy) > 0 {
		return gBlocked, sgrBlocked
	}
	switch t.Priority {
	case task.Highest:
		return gHighest, sgrHighest
	case task.High:
		return gHigh, sgrHigh
	case task.Medium:
		return gMedium, sgrMedium
	case task.Low:
		return gLow, sgrLow
	case task.Lowest:
		return gLowest, sgrLow
	}
	return gNormal, ""
}

// pad/padLeft take both the styled and the plain string so escape sequences do
// not count towards the column width.
func pad(styled, plain string, w int) string {
	if n := w - width(plain); n > 0 {
		return styled + strings.Repeat(" ", n)
	}
	return styled
}

func padLeft(styled, plain string, w int) string {
	if n := w - width(plain); n > 0 {
		return strings.Repeat(" ", n) + styled
	}
	return styled
}

func total(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

func reasons(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", m[k], k))
	}
	return strings.Join(parts, ", ")
}

func truncate(s string, w int) string {
	if width(s) <= w {
		return s
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := runeWidth(r)
		if used+rw > w-1 {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + "…"
}

// width is a display-width estimate: enough to keep columns aligned for the
// Latin text and single-width glyphs tuido emits, and to degrade gracefully if
// a description contains something wider.
func width(s string) int {
	n := 0
	for _, r := range s {
		n += runeWidth(r)
	}
	return n
}

func runeWidth(r rune) int {
	switch {
	case r == 0xFE0F: // variation selector: zero width
		return 0
	case r == '✓', r == '✔', r == '✗', r == '✘':
		return 1 // text-presentation dingbats, narrow in every terminal we target
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0xA4CF, // CJK
		r >= 0xAC00 && r <= 0xD7A3, // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFF00 && r <= 0xFF60,
		r >= 0x1F300 && r <= 0x1FAFF, // emoji
		r >= 0x2600 && r <= 0x27BF:   // misc symbols
		return 2
	}
	return 1
}
