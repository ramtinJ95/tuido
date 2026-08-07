// Package sortkey orders tasks within a block.
//
// It never moves a line across a fence: headings, prose, blank lines and code
// fences are boundaries the user wrote, and they are inviolable.
package sortkey

import (
	"fmt"
	"sort"

	"github.com/ramtinJ95/tuido/internal/task"
)

// Mode selects the comparator.
type Mode string

const (
	ByPrio    Mode = "prio"    // the default: state, blocked, priority, due, created
	ByDue     Mode = "due"     // state, blocked, due, priority, created
	ByCreated Mode = "created" // state, blocked, created
	None      Mode = "none"    // do not sort this file at all
)

// ParseMode validates a --by value or a marker's sort= value.
func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case ByPrio, ByDue, ByCreated, None:
		return Mode(s), nil
	case "":
		return ByPrio, nil
	}
	return "", fmt.Errorf("unknown sort mode %q (prio|due|created|none)", s)
}

// ModeFor picks the mode for a file: an explicit --by wins, then the file's
// marker comment, then the default.
func ModeFor(f *task.File, override Mode) (Mode, error) {
	if override != "" {
		return ParseMode(string(override))
	}
	return ParseMode(f.Marker.Sort)
}

// File sorts every block in f and reports how many top-level items moved.
//
// The sort is stable and the comparator is a total order (the final tiebreak is
// the original position), so sorting twice produces identical bytes.
func File(f *task.File, mode Mode, openIDs map[string]bool) int {
	if mode == None {
		return 0
	}
	moved := 0
	// Blocks are re-derived after each reorder, and line indices shift, so walk
	// by index and re-read.
	for bi := 0; bi < len(f.Blocks); bi++ {
		b := f.Blocks[bi]
		if len(b.Items) < 2 {
			continue
		}
		perm := make([]int, len(b.Items))
		for i := range perm {
			perm[i] = i
		}
		sort.SliceStable(perm, func(x, y int) bool {
			return less(b.Items[perm[x]].Task, b.Items[perm[y]].Task, mode, openIDs)
		})
		moved += f.ReorderBlock(b, perm)
	}
	return moved
}

// less implements the comparator. Keys 1 and 2 apply in every mode: finished
// work sinks, and a blocked task never sorts to the top just because it is
// urgent.
func less(a, b *task.Task, mode Mode, openIDs map[string]bool) bool {
	if c := cmp(closedRank(a), closedRank(b)); c != 0 {
		return c < 0
	}

	// Closed tasks: most recently finished nearest the open work.
	if a.State.Closed() {
		return closedDate(a).Compare(closedDate(b)) > 0
	}

	if c := cmp(blockedRank(a, openIDs), blockedRank(b, openIDs)); c != 0 {
		return c < 0
	}

	switch mode {
	case ByCreated:
		return created(a).Compare(created(b)) < 0
	case ByDue:
		if c := due(a).Compare(due(b)); c != 0 {
			return c < 0
		}
		if c := cmp(a.Priority.Rank(), b.Priority.Rank()); c != 0 {
			return c < 0
		}
		return created(a).Compare(created(b)) < 0
	default: // ByPrio
		if c := cmp(a.Priority.Rank(), b.Priority.Rank()); c != 0 {
			return c < 0
		}
		if c := due(a).Compare(due(b)); c != 0 {
			return c < 0
		}
		return created(a).Compare(created(b)) < 0
	}
}

func closedRank(t *task.Task) int {
	if t.State.Closed() {
		return 1
	}
	return 0
}

func blockedRank(t *task.Task, openIDs map[string]bool) int {
	if t.Blocked(openIDs) {
		return 1
	}
	return 0
}

// far is the sentinel for a missing date: it sorts last, which is what "no due
// date" should mean next to a dated task.
var far = task.Date{Year: 9999, Month: 12, Day: 31}

func due(t *task.Task) task.Date {
	if t.Due == nil {
		return far
	}
	return *t.Due
}

// created falls back to "today" so an undated task sorts after everything with
// a real creation stamp — the same "unknown sorts last" rule as due.
func created(t *task.Task) task.Date {
	if t.Created == nil {
		return far
	}
	return *t.Created
}

func closedDate(t *task.Task) task.Date {
	switch {
	case t.Completed != nil:
		return *t.Completed
	case t.CancelledOn != nil:
		return *t.CancelledOn
	}
	return task.Date{}
}

func cmp(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
