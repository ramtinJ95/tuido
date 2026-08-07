package task

import "strings"

// Canonical renders the task in tuido's field order.
//
// It is used only for lines tuido created or mutated. Clean lines are emitted
// byte-for-byte as read, so hand-written field order elsewhere in the file is
// never disturbed.
//
//	{indent}- [{state}] {desc} {prio} {🔁} {🛫} {⏳} {📅} {🆔} {⛔} {🏁} {➕} {✅|❌}
func (t *Task) Canonical() string {
	var b strings.Builder
	b.WriteString(t.Indent)
	bullet := t.Bullet
	if bullet == "" {
		bullet = "-"
	}
	b.WriteString(bullet)
	b.WriteString(" [")
	b.WriteString(t.State.Char())
	b.WriteString("]")

	add := func(parts ...string) {
		for _, p := range parts {
			if p == "" {
				return
			}
		}
		b.WriteString(" ")
		b.WriteString(strings.Join(parts, " "))
	}
	date := func(marker string, d *Date) {
		if d != nil {
			add(marker, d.String())
		}
	}

	add(t.Desc)
	add(t.Priority.Marker())
	add(mRecur, t.Recurrence)
	date(mStart, t.Start)
	date(mScheduled, t.Scheduled)
	date(mDue, t.Due)
	add(mID, t.ID)
	add(mBlockedBy, strings.Join(t.BlockedBy, ","))
	add(mOnCompletion, t.OnCompletion)
	date(mCreated, t.Created)
	date(mDone, t.Completed)
	date(mCancelled, t.CancelledOn)

	return b.String()
}
