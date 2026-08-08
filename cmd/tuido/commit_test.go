package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitOut(t *testing.T, root string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestCommitPlumbingCommitsOneFile(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", "--root", e.root, "--workspace", "work", "--git")
	gitOut(t, e.root, "config", "user.name", "tuido")
	gitOut(t, e.root, "config", "user.email", "tuido@example.com")

	// A brand-new untracked file, as if just saved from the editor.
	e.write("work/data.md", "- [ ] typed in the editor\n")
	e.mustRun("_commit", filepath.Join(e.root, "work", "data.md"))

	log := gitOut(t, e.root, "log", "--oneline")
	if !strings.Contains(log, "edit: work/data") {
		t.Errorf("commit missing from log:\n%s", log)
	}
	// Only the named file is committed; init's untracked inbox is not swept up.
	if st := gitOut(t, e.root, "status", "--short"); strings.Contains(st, "data.md") {
		t.Errorf("data.md still dirty after _commit:\n%s", st)
	}

	// Saving again with no changes must not create an empty commit.
	before := gitOut(t, e.root, "rev-parse", "HEAD")
	e.mustRun("_commit", filepath.Join(e.root, "work", "data.md"))
	if after := gitOut(t, e.root, "rev-parse", "HEAD"); after != before {
		t.Error("no-op save created a commit")
	}
}

func TestCommitPlumbingRefusesPathsOutsideRoot(t *testing.T) {
	e := newEnv(t)
	e.init()
	r := e.run("_commit", "/etc/hosts")
	if r.code != 1 {
		t.Errorf("path outside root exited %d, want 1\nstderr: %s", r.code, r.stderr)
	}
}
