package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveSweepsClosedTasks(t *testing.T) {
	e := newEnv(t)
	e.init()
	e.write("work/oncall.md",
		"# now\n\n- [x] drain the pool ✅ 2026-08-01\n- [ ] refill the pool\n\n# next\n\n- [-] cancelled idea ❌ 2026-08-02\n")

	out := e.mustRun("archive", "oncall").stdout
	if !strings.Contains(out, "✓ archived 2 from work/oncall → _archive/oncall.md") {
		t.Fatalf("unexpected output:\n%s", out)
	}

	src := e.read("work/oncall.md")
	if strings.Contains(src, "drain the pool") || strings.Contains(src, "cancelled idea") {
		t.Errorf("closed tasks still in source:\n%s", src)
	}
	if !strings.Contains(src, "refill the pool") {
		t.Errorf("open task removed from source:\n%s", src)
	}

	arc := e.read("work/_archive/oncall.md")
	want := "# now\n- [x] drain the pool ✅ 2026-08-01\n\n# next\n- [-] cancelled idea ❌ 2026-08-02\n"
	if arc != want {
		t.Errorf("archive = %q, want %q", arc, want)
	}
}

func TestArchiveSkipsDoneParentWithOpenChild(t *testing.T) {
	e := newEnv(t)
	e.init()
	e.write("work/inbox.md",
		"- [x] parent ✅ 2026-08-01\n  - [ ] child\n")

	out := e.mustRun("archive", "inbox").stdout
	if !strings.Contains(out, "skipped in work/inbox: parent — open subtask: child") {
		t.Errorf("skip not reported:\n%s", out)
	}
	if !strings.Contains(out, "· nothing to archive") {
		t.Errorf("expected nothing-to-archive footer:\n%s", out)
	}
	if !strings.Contains(e.read("work/inbox.md"), "parent") {
		t.Error("skipped parent was removed")
	}
}

func TestArchiveDryRunWritesNothing(t *testing.T) {
	e := newEnv(t)
	e.init()
	body := "- [x] finished ✅ 2026-08-01\n"
	e.write("work/inbox.md", body)

	out := e.mustRun("archive", "inbox", "--dry-run").stdout
	if !strings.Contains(out, "· would archive 1 from work/inbox → _archive/inbox.md") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	if got := e.read("work/inbox.md"); got != body {
		t.Errorf("dry run changed the source: %q", got)
	}
	if _, err := os.Stat(filepath.Join(e.root, "work", "_archive")); !os.IsNotExist(err) {
		t.Errorf("dry run created the archive dir: %v", err)
	}
}

// The full loop: done marks, archive moves, plain ls no longer sees the list's
// mirror, ls --archived does.
func TestDoneArchiveLsRoundTrip(t *testing.T) {
	e := newEnv(t)
	e.init()
	e.write("work/inbox.md", "- [ ] ship the archive feature\n- [ ] something else\n")

	e.mustRun("done", "ship the archive feature")
	e.mustRun("archive", "inbox")

	out := e.mustRun("ls").stdout
	if strings.Contains(out, "ship the archive feature") || strings.Contains(out, "_archive") {
		t.Errorf("plain ls sees archived content:\n%s", out)
	}

	out = e.mustRun("ls", "--archived").stdout
	if !strings.Contains(out, "work/_archive/inbox") || !strings.Contains(out, "ship the archive feature") {
		t.Errorf("ls --archived missing the moved task:\n%s", out)
	}

	// Scope-first spelling works like the other scoped commands.
	out = e.mustRun("work/inbox", "archive").stdout
	if !strings.Contains(out, "· nothing to archive") {
		t.Errorf("scope-first archive:\n%s", out)
	}
}
