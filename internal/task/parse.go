package task

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Field markers. Every one is a single rune, optionally followed by U+FE0F.
const (
	mHighest      = "\U0001F53A" // 🔺
	mHigh         = "⏫"          // ⏫
	mMedium       = "\U0001F53C" // 🔼
	mLow          = "\U0001F53D" // 🔽
	mLowest       = "⏬"          // ⏬
	mCreated      = "➕"          // ➕
	mStart        = "\U0001F6EB" // 🛫
	mScheduled    = "⏳"          // ⏳
	mDue          = "\U0001F4C5" // 📅
	mDone         = "✅"          // ✅
	mCancelled    = "❌"          // ❌
	mRecur        = "\U0001F501" // 🔁
	mID           = "\U0001F194" // 🆔
	mBlockedBy    = "⛔"          // ⛔
	mOnCompletion = "\U0001F3C1" // 🏁

	// U+FE0F may or may not follow an emoji; U+00A0 is often used as the
	// separator. Both are spec footguns called out in the Tasks docs.
	variationSelector = '\uFE0F'
	nbsp              = "\u00a0"
)

var markerRunes = map[rune]string{
	[]rune(mHighest)[0]:      mHighest,
	[]rune(mHigh)[0]:         mHigh,
	[]rune(mMedium)[0]:       mMedium,
	[]rune(mLow)[0]:          mLow,
	[]rune(mLowest)[0]:       mLowest,
	[]rune(mCreated)[0]:      mCreated,
	[]rune(mStart)[0]:        mStart,
	[]rune(mScheduled)[0]:    mScheduled,
	[]rune(mDue)[0]:          mDue,
	[]rune(mDone)[0]:         mDone,
	[]rune(mCancelled)[0]:    mCancelled,
	[]rune(mRecur)[0]:        mRecur,
	[]rune(mID)[0]:           mID,
	[]rune(mBlockedBy)[0]:    mBlockedBy,
	[]rune(mOnCompletion)[0]: mOnCompletion,
}

// LineKind classifies a line of a task file.
type LineKind uint8

const (
	LineOther      LineKind = iota // prose, anything unrecognised
	LineBlank                      //
	LineHeading                    // ^#{1,6}\s
	LineMarker                     // <!-- tuido: ... -->
	LineFenceDelim                 // ``` or ~~~
	LineInFence                    // inside a fenced code block
	LineTask                       // - [ ] ...
	LineTaskCont                   // indented continuation of the task above
)

// Line is one line of a file. Raw excludes the terminator; EOL holds it, and is
// "" for a final line with no trailing newline. Together they round-trip
// exactly, including CRLF files.
type Line struct {
	Kind LineKind
	Raw  string
	EOL  string
	Task *Task // non-nil iff Kind == LineTask
}

// File is a parsed task file.
type File struct {
	Path   string
	Marker Marker
	Lines  []Line
	Blocks []Block

	eol string // dominant terminator, used for lines tuido appends
}

// Marker is the per-file `<!-- tuido: ... -->` configuration comment.
type Marker struct {
	Present bool
	Line    int               // 0-based index into File.Lines
	Sort    string            // prio | due | created | none
	Capture string            // heading name
	Keys    map[string]string // every key, including unrecognised ones
}

// ErrConflicted is returned when a file contains git conflict markers. Nothing
// may parse or rewrite such a file: sorting `<<<<<<< HEAD` into a task list is
// the worst available outcome.
type ErrConflicted struct {
	Path string
	Line int // 1-based
}

func (e *ErrConflicted) Error() string {
	return fmt.Sprintf("%s:%d: unresolved git conflict — resolve it in your editor first", e.Path, e.Line)
}

var (
	taskRE     = regexp.MustCompile(`^([ \t]*)([-*+]) \[([ xX/\-])\](?: (.*))?$`)
	headingRE  = regexp.MustCompile(`^#{1,6}(\s|$)`)
	markerRE   = regexp.MustCompile(`^<!--\s*tuido:\s*(.*?)\s*-->\s*$`)
	fenceRE    = regexp.MustCompile("^([ \t]*)(```+|~~~+)(.*)$")
	tagRE      = regexp.MustCompile(`(^|[^\p{L}\p{N}_/#])#([\p{L}\p{N}_/-]*[\p{L}_/-][\p{L}\p{N}_/-]*)`)
	conflictRE = regexp.MustCompile(`^(<{7}|={7}|>{7})(\s|$)`)
)

// Parse classifies every line of b and extracts the tasks.
func Parse(path string, b []byte) (*File, error) {
	f := &File{Path: path, eol: "\n"}
	f.Lines = splitLines(b)
	if bytes.Contains(b, []byte("\r\n")) {
		f.eol = "\r\n"
	}

	// Conflict markers first: never classify a conflicted file.
	for i := range f.Lines {
		if conflictRE.MatchString(f.Lines[i].Raw) {
			return nil, &ErrConflicted{Path: path, Line: i + 1}
		}
	}

	var (
		inFence   bool
		fenceMark string
		seenBody  bool // any non-blank line yet, for marker position
	)
	for i := range f.Lines {
		ln := &f.Lines[i]
		raw := ln.Raw

		if inFence {
			ln.Kind = LineInFence
			if m := fenceRE.FindStringSubmatch(raw); m != nil && strings.HasPrefix(m[2], fenceMark[:1]) &&
				len(m[2]) >= len(fenceMark) && strings.TrimSpace(m[3]) == "" {
				ln.Kind = LineFenceDelim
				inFence = false
			}
			seenBody = true
			continue
		}

		switch {
		case strings.TrimSpace(raw) == "":
			ln.Kind = LineBlank
			continue // does not count as body, so a marker may follow blank lines
		case fenceRE.MatchString(raw):
			m := fenceRE.FindStringSubmatch(raw)
			ln.Kind = LineFenceDelim
			inFence = true
			fenceMark = m[2]
		case !seenBody && markerRE.MatchString(raw):
			ln.Kind = LineMarker
			f.Marker = parseMarker(markerRE.FindStringSubmatch(raw)[1])
			f.Marker.Present = true
			f.Marker.Line = i
		case headingRE.MatchString(raw):
			ln.Kind = LineHeading
		default:
			if m := taskRE.FindStringSubmatch(raw); m != nil {
				ln.Kind = LineTask
				ln.Task = newTask(path, i+1, raw, m)
			} else if k := contKind(f.Lines, i); k == LineTaskCont {
				ln.Kind = LineTaskCont
			} else {
				ln.Kind = LineOther
			}
		}
		seenBody = true
	}

	f.Blocks = findBlocks(f.Lines)
	return f, nil
}

// contKind decides whether line i belongs to the task above it.
//
// The test is deliberately coarse — any indented line directly following a task
// or another continuation — because the only question that matters downstream is
// "does this line travel with that item when the block is sorted?". Unindented
// prose after a task is not a continuation: it is a fence, and it ends the
// block.
func contKind(lines []Line, i int) LineKind {
	if i == 0 || indentWidth(leadingSpace(lines[i].Raw)) == 0 {
		return LineOther
	}
	switch lines[i-1].Kind {
	case LineTask, LineTaskCont:
		return LineTaskCont
	}
	return LineOther
}

func newTask(path string, line int, raw string, m []string) *Task {
	t := &Task{
		State:  stateFromChar(m[3][0]),
		Indent: m[1],
		Bullet: m[2],
		Path:   path,
		Line:   line,
		raw:    raw,
	}
	t.Desc = t.parseFields(m[4])
	t.Tags = extractTags(t.Desc)
	return t
}

// Bytes serialises the file. Clean lines are emitted verbatim; only dirty tasks
// are re-serialised, which is what keeps diffs the size of the actual change.
func (f *File) Bytes() []byte {
	var b bytes.Buffer
	for _, ln := range f.Lines {
		if ln.Task != nil {
			b.WriteString(ln.Task.Text())
		} else {
			b.WriteString(ln.Raw)
		}
		b.WriteString(ln.EOL)
	}
	return b.Bytes()
}

// Dirty reports whether any task in the file was mutated.
func (f *File) Dirty() bool {
	for _, ln := range f.Lines {
		if ln.Task != nil && ln.Task.dirty {
			return true
		}
	}
	return false
}

// Tasks returns every task in file order.
func (f *File) Tasks() []*Task {
	var out []*Task
	for _, ln := range f.Lines {
		if ln.Task != nil {
			out = append(out, ln.Task)
		}
	}
	return out
}

// EOL is the terminator to use for lines appended to this file.
func (f *File) EOL() string { return f.eol }

// NewLine builds a Line holding a freshly created (and therefore dirty) task.
func (f *File) NewLine(t *Task) Line {
	t.dirty = true
	return Line{Kind: LineTask, Raw: t.Canonical(), EOL: f.eol, Task: t}
}

// Insert splices lines in at index i, which may be len(f.Lines).
func (f *File) Insert(i int, lines ...Line) {
	// A file whose last line lacked a terminator needs one before anything can
	// follow it.
	if n := len(f.Lines); n > 0 && i >= n && f.Lines[n-1].EOL == "" {
		f.Lines[n-1].EOL = f.eol
	}
	f.Lines = append(f.Lines[:i], append(append([]Line{}, lines...), f.Lines[i:]...)...)
	f.renumber()
	f.Blocks = findBlocks(f.Lines)
}

func (f *File) renumber() {
	for i := range f.Lines {
		if f.Lines[i].Task != nil {
			f.Lines[i].Task.Line = i + 1
		}
	}
}

func splitLines(b []byte) []Line {
	if len(b) == 0 {
		return nil
	}
	var out []Line
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] != '\n' {
			continue
		}
		end, eol := i, "\n"
		if i > start && b[i-1] == '\r' {
			end, eol = i-1, "\r\n"
		}
		out = append(out, Line{Raw: string(b[start:end]), EOL: eol})
		start = i + 1
	}
	if start < len(b) {
		out = append(out, Line{Raw: string(b[start:]), EOL: ""})
	}
	return out
}

func parseMarker(body string) Marker {
	m := Marker{Keys: map[string]string{}}
	for _, kv := range strings.Fields(body) {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		m.Keys[k] = v
		switch k {
		case "sort":
			m.Sort = v
		case "capture":
			m.Capture = v
		}
	}
	return m
}

func leadingSpace(s string) string {
	return s[:len(s)-len(strings.TrimLeft(s, " \t"))]
}

// indentWidth expands tabs to 4 columns so mixed indentation still nests
// predictably.
func indentWidth(s string) int {
	w := 0
	for _, r := range s {
		if r == '\t' {
			w += 4 - w%4
		} else {
			w++
		}
	}
	return w
}

func extractTags(desc string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range tagRE.FindAllStringSubmatch(desc, -1) {
		t := m[2]
		if seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	return out
}

// segment is one recognised marker and the text that follows it, up to the next
// marker or end of line.
type segment struct {
	marker   string
	start    int // byte offset of the marker
	valStart int // byte offset just past the marker (and its variation selector)
	end      int // byte offset of the next marker, or len(rest)
}

// scanSegments finds every field marker in rest. A marker only counts at a word
// boundary, so an emoji glued to a word stays part of the description.
func scanSegments(rest string) []segment {
	var segs []segment
	boundary := true
	for i := 0; i < len(rest); {
		r, sz := utf8.DecodeRuneInString(rest[i:])
		if boundary {
			if m, ok := markerRunes[r]; ok {
				valStart := i + sz
				if r2, sz2 := utf8.DecodeRuneInString(rest[valStart:]); r2 == variationSelector {
					valStart += sz2
				}
				segs = append(segs, segment{marker: m, start: i, valStart: valStart})
			}
		}
		boundary = unicode.IsSpace(r) // IsSpace covers U+00A0
		i += sz
	}
	for i := range segs {
		if i+1 < len(segs) {
			segs[i].end = segs[i+1].start
		} else {
			segs[i].end = len(rest)
		}
	}
	return segs
}

// HasFieldMarker reports whether s contains a field marker at a word boundary
// — the same test Rewritable applies to a task's free-text fields.
func HasFieldMarker(s string) bool {
	return len(scanSegments(" "+s)) > 0
}

// parseFields pulls the emoji fields out of the text after the checkbox and
// returns the description.
//
// A marker whose value does not parse is not a field: it stays in the
// description verbatim. That keeps malformed input visible and preserved rather
// than silently reinterpreted, and it means a duplicate marker never overwrites
// the first one.
func (t *Task) parseFields(rest string) string {
	segs := scanSegments(rest)
	if len(segs) == 0 {
		return strings.TrimSpace(rest)
	}

	var parts []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			parts = append(parts, s)
		}
	}
	add(rest[:segs[0].start])

	prioSet := false
	for _, s := range segs {
		val := rest[s.valStart:s.end]
		literal := rest[s.start:s.end]

		setPrio := func(p Priority) {
			if prioSet {
				add(literal)
				return
			}
			t.Priority, prioSet = p, true
			add(val)
		}
		setDate := func(field **Date) {
			if *field != nil {
				add(literal)
				return
			}
			tok, tail := cutToken(val)
			d, ok := ParseDate(tok)
			if !ok {
				add(literal)
				return
			}
			*field = &d
			add(tail)
		}

		switch s.marker {
		case mHighest:
			setPrio(Highest)
		case mHigh:
			setPrio(High)
		case mMedium:
			setPrio(Medium)
		case mLow:
			setPrio(Low)
		case mLowest:
			setPrio(Lowest)
		case mCreated:
			setDate(&t.Created)
		case mStart:
			setDate(&t.Start)
		case mScheduled:
			setDate(&t.Scheduled)
		case mDue:
			setDate(&t.Due)
		case mDone:
			setDate(&t.Completed)
		case mCancelled:
			setDate(&t.CancelledOn)
		case mRecur:
			if t.Recurrence != "" || strings.TrimSpace(val) == "" {
				add(literal)
				continue
			}
			t.Recurrence = strings.TrimSpace(val)
		case mID:
			tok, tail := cutToken(val)
			if t.ID != "" || tok == "" {
				add(literal)
				continue
			}
			t.ID = tok
			add(tail)
		case mBlockedBy:
			tok, tail := cutToken(val)
			if len(t.BlockedBy) > 0 || tok == "" {
				add(literal)
				continue
			}
			for _, id := range strings.Split(tok, ",") {
				if id = strings.TrimSpace(id); id != "" {
					t.BlockedBy = append(t.BlockedBy, id)
				}
			}
			add(tail)
		case mOnCompletion:
			tok, tail := cutToken(val)
			lower := strings.ToLower(tok)
			if t.OnCompletion != "" || (lower != "keep" && lower != "delete") {
				add(literal)
				continue
			}
			t.OnCompletion = lower
			add(tail)
		}
	}
	return strings.Join(parts, " ")
}

// cutToken splits off the first whitespace-delimited token.
func cutToken(s string) (tok, rest string) {
	s = strings.TrimLeft(s, " \t"+nbsp)
	for i, r := range s {
		if unicode.IsSpace(r) {
			return s[:i], s[i:]
		}
	}
	return s, ""
}
