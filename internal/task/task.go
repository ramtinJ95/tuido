// Package task parses, mutates and serialises markdown task lines in the
// Obsidian Tasks emoji dialect.
//
// It is pure: bytes in, bytes out. It must not import any other internal
// package. Its primary guarantee is round-trip fidelity — parsing a file and
// writing it back is byte-identical unless a task was deliberately mutated.
package task

import (
	"fmt"
	"strings"
	"time"
)

// State is the checkbox state of a task.
type State uint8

const (
	Open       State = iota // [ ]
	InProgress              // [/]
	Done                    // [x]
	Cancelled               // [-]
)

// Char returns the character that goes between the brackets.
func (s State) Char() string {
	switch s {
	case InProgress:
		return "/"
	case Done:
		return "x"
	case Cancelled:
		return "-"
	default:
		return " "
	}
}

// Closed reports whether the task is no longer actionable.
func (s State) Closed() bool { return s == Done || s == Cancelled }

func (s State) String() string {
	switch s {
	case InProgress:
		return "in progress"
	case Done:
		return "done"
	case Cancelled:
		return "cancelled"
	default:
		return "open"
	}
}

func stateFromChar(c byte) State {
	switch c {
	case 'x', 'X':
		return Done
	case '/':
		return InProgress
	case '-':
		return Cancelled
	default:
		return Open
	}
}

// Priority is the task priority.
//
// Normal is the zero value because it is the absence of a marker: a task built
// from nothing must not come out as Highest. The sort order lives in Rank
// instead of in the constant values, so no code path can forget to initialise
// this field.
type Priority uint8

const (
	Normal  Priority = iota // (no marker)
	Highest                 // 🔺
	High                    // ⏫
	Medium                  // 🔼
	Low                     // 🔽
	Lowest                  // ⏬
)

// Rank orders priorities for sorting: Highest is 0, Lowest is 5, and Normal
// sits in the middle where the absence of a marker belongs.
func (p Priority) Rank() int {
	switch p {
	case Highest:
		return 0
	case High:
		return 1
	case Medium:
		return 2
	case Low:
		return 4
	case Lowest:
		return 5
	default:
		return 3
	}
}

// Marker returns the emoji for the priority, or "" for Normal.
func (p Priority) Marker() string {
	switch p {
	case Highest:
		return mHighest
	case High:
		return mHigh
	case Medium:
		return mMedium
	case Low:
		return mLow
	case Lowest:
		return mLowest
	default:
		return ""
	}
}

func (p Priority) String() string {
	switch p {
	case Highest:
		return "highest"
	case High:
		return "high"
	case Medium:
		return "medium"
	case Low:
		return "low"
	case Lowest:
		return "lowest"
	default:
		return "normal"
	}
}

// ParsePriority accepts the names used on the command line.
func ParsePriority(s string) (Priority, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "highest", "urgent":
		return Highest, nil
	case "high":
		return High, nil
	case "medium", "med":
		return Medium, nil
	case "normal", "none", "":
		return Normal, nil
	case "low":
		return Low, nil
	case "lowest":
		return Lowest, nil
	}
	return Normal, fmt.Errorf("unknown priority %q (highest|high|medium|normal|low|lowest)", s)
}

// Date is a calendar date with no time and no zone, which is all the dialect
// records. Comparison is therefore total and unambiguous.
type Date struct {
	Year  int
	Month int
	Day   int
}

// ParseDate parses YYYY-MM-DD strictly: it rejects anything time.Parse would
// normalise, so 2026-02-31 is not silently accepted as March 3rd.
func ParseDate(s string) (Date, bool) {
	if len(s) != 10 {
		return Date{}, false
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return Date{}, false
	}
	if t.Format("2006-01-02") != s {
		return Date{}, false
	}
	return Date{t.Year(), int(t.Month()), t.Day()}, true
}

func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, d.Month, d.Day)
}

// Zero reports whether the date is unset.
func (d Date) Zero() bool { return d == Date{} }

// Time returns midnight local time on that date.
func (d Date) Time() time.Time {
	return time.Date(d.Year, time.Month(d.Month), d.Day, 0, 0, 0, 0, time.Local)
}

// Compare orders dates chronologically.
func (d Date) Compare(o Date) int {
	switch {
	case d.Year != o.Year:
		return cmpInt(d.Year, o.Year)
	case d.Month != o.Month:
		return cmpInt(d.Month, o.Month)
	default:
		return cmpInt(d.Day, o.Day)
	}
}

// DaysSince returns the whole days between d and o (positive if o is later).
func (d Date) DaysSince(o Date) int {
	return int(o.Time().Sub(d.Time()).Hours() / 24)
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// Today is the current local date.
func Today() Date {
	n := time.Now()
	return Date{n.Year(), int(n.Month()), n.Day()}
}

// DateFromTime truncates a time to a Date.
func DateFromTime(t time.Time) Date {
	return Date{t.Year(), int(t.Month()), t.Day()}
}

// Task is one markdown task line.
//
// raw holds the original bytes. Only a task marked dirty is re-serialised, so
// hand-written field order, unusual bullets and unknown emoji survive untouched
// in every file tuido writes.
type Task struct {
	State    State
	Desc     string // description, #tags retained inline, emoji fields stripped
	Priority Priority
	Tags     []string // derived view of #tags for filtering; Desc stays canonical

	Created, Start, Scheduled, Due, Completed, CancelledOn *Date

	Recurrence   string   // 🔁 raw rule text, preserved but not yet interpreted
	ID           string   // 🆔
	BlockedBy    []string // ⛔
	OnCompletion string   // 🏁

	Indent string // leading whitespace, verbatim
	Bullet string // "-", "*" or "+", verbatim

	// Path and Line locate the task for error messages and fzf. Set by callers
	// that know them; Parse fills them in.
	Path string
	Line int // 1-based

	raw   string
	dirty bool
}

// Raw returns the original line bytes, without the line terminator.
func (t *Task) Raw() string { return t.raw }

// Dirty reports whether the task has been mutated and so will be re-serialised.
func (t *Task) Dirty() bool { return t.dirty }

// Text returns the line as it should be written: the original bytes when
// clean, canonical emission when dirty.
func (t *Task) Text() string {
	if !t.dirty {
		return t.raw
	}
	return t.Canonical()
}

// SetState changes the checkbox state.
func (t *Task) SetState(s State) {
	if t.State == s {
		return
	}
	t.State = s
	t.dirty = true
}

// SetPriority changes the priority.
func (t *Task) SetPriority(p Priority) {
	if t.Priority == p {
		return
	}
	t.Priority = p
	t.dirty = true
}

// SetDue sets or clears the due date.
func (t *Task) SetDue(d *Date) { t.setDate(&t.Due, d) }

// SetCompleted sets or clears the completion date.
func (t *Task) SetCompleted(d *Date) { t.setDate(&t.Completed, d) }

// SetCancelledOn sets or clears the cancellation date.
func (t *Task) SetCancelledOn(d *Date) { t.setDate(&t.CancelledOn, d) }

// SetCreated sets or clears the creation date.
func (t *Task) SetCreated(d *Date) { t.setDate(&t.Created, d) }

func (t *Task) setDate(field **Date, d *Date) {
	switch {
	case *field == nil && d == nil:
		return
	case *field != nil && d != nil && **field == *d:
		return
	}
	*field = d
	t.dirty = true
}

// SetStart sets or clears the start date.
func (t *Task) SetStart(d *Date) { t.setDate(&t.Start, d) }

// SetScheduled sets or clears the scheduled date.
func (t *Task) SetScheduled(d *Date) { t.setDate(&t.Scheduled, d) }

// SetDesc replaces the description. Tags is re-derived, since Desc is
// authoritative for it.
func (t *Task) SetDesc(desc string) {
	if t.Desc == desc {
		return
	}
	t.Desc = desc
	t.Tags = extractTags(t.Desc)
	t.dirty = true
}

// SetID sets the 🆔 field.
func (t *Task) SetID(id string) {
	if t.ID == id {
		return
	}
	t.ID = id
	t.dirty = true
}

// AddTag appends " #tag" to the description if it is not already present.
// Desc is authoritative; Tags is re-derived from it.
func (t *Task) AddTag(tag string) {
	tag = strings.TrimPrefix(strings.TrimSpace(tag), "#")
	if tag == "" {
		return
	}
	for _, existing := range t.Tags {
		if strings.EqualFold(existing, tag) {
			return
		}
	}
	if t.Desc == "" {
		t.Desc = "#" + tag
	} else {
		t.Desc += " #" + tag
	}
	t.Tags = extractTags(t.Desc)
	t.dirty = true
}

// HasTag reports whether the task carries the tag, case-insensitively and
// ignoring a leading '#'.
func (t *Task) HasTag(tag string) bool {
	tag = strings.TrimPrefix(strings.TrimSpace(tag), "#")
	for _, existing := range t.Tags {
		if strings.EqualFold(existing, tag) {
			return true
		}
	}
	return false
}

// Rewritable reports whether the task can safely be re-serialised.
//
// A description that still contains a field marker at a word boundary — a
// duplicate ⏫, or a 📅 whose date was malformed and so stayed in the text —
// cannot round-trip through canonical emission: rewriting it would move the
// stray marker and the next parse would read it differently. Rather than
// silently mangle such a line, every mutating command refuses it and points at
// the file, which is the "fail loudly, never guess" principle applied to the
// one case where tuido genuinely cannot tell what the author meant.
// The check covers every free-text field, not just the description: a marker
// glued to the end of a recurrence rule becomes a field of its own once
// canonical emission inserts the separating space.
func (t *Task) Rewritable() error {
	check := func(where, s string) error {
		if segs := scanSegments(" " + s); len(segs) > 0 {
			return fmt.Errorf("%s:%d: %s contains an unparsed %s marker — fix the line in your editor first",
				t.Path, t.Line, where, segs[0].marker)
		}
		return nil
	}
	for _, c := range []struct{ where, s string }{
		{"description", t.Desc},
		{"recurrence rule", t.Recurrence},
		{"id", t.ID},
		{"blocked-by list", strings.Join(t.BlockedBy, ",")},
	} {
		if err := check(c.where, c.s); err != nil {
			return err
		}
	}
	return nil
}

// Blocked reports whether any of the task's blockers is still open. openIDs is
// the set of ids belonging to open or in-progress tasks in scope.
func (t *Task) Blocked(openIDs map[string]bool) bool {
	for _, id := range t.BlockedBy {
		if openIDs[id] {
			return true
		}
	}
	return false
}

// Hidden reports why a task should not appear in a default `ls`, or "" if it
// should. The reasons are the ones counted in the footer.
func (t *Task) Hidden(today Date, openIDs map[string]bool) string {
	switch {
	case t.State == Done:
		return "done"
	case t.State == Cancelled:
		return "cancelled"
	case t.Blocked(openIDs):
		return "blocked"
	case t.Start != nil && today.Compare(*t.Start) < 0:
		return "not started"
	case t.Scheduled != nil && today.Compare(*t.Scheduled) < 0:
		return "scheduled"
	}
	return ""
}
