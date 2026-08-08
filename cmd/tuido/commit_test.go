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

	// A separately staged file must stay staged and out of the save commit.
	e.write("work/other.md", "- [ ] staged separately\n")
	gitOut(t, e.root, "add", "work/other.md")

	// A brand-new untracked file, as if just saved from the editor.
	e.write("work/data.md", "- [ ] typed in the editor\n")
	e.mustRun("_commit", filepath.Join(e.root, "work", "data.md"))

	log := gitOut(t, e.root, "log", "--oneline")
	if !strings.Contains(log, "edit: work/data") {
		t.Errorf("commit missing from log:\n%s", log)
	}
	if names := strings.TrimSpace(gitOut(t, e.root, "show", "--format=", "--name-only", "HEAD")); names != "work/data.md" {
		t.Errorf("commit contains %q, want only work/data.md", names)
	}
	// Only the named file is committed; staged and untracked files are not swept up.
	if st := gitOut(t, e.root, "status", "--short"); strings.Contains(st, "data.md") {
		t.Errorf("data.md still dirty after _commit:\n%s", st)
	} else if !strings.Contains(st, "A  work/other.md") {
		t.Errorf("other.md no longer staged after _commit:\n%s", st)
	}

	// Saving again with no changes must not create an empty commit.
	before := gitOut(t, e.root, "rev-parse", "HEAD")
	e.mustRun("_commit", filepath.Join(e.root, "work", "data.md"))
	if after := gitOut(t, e.root, "rev-parse", "HEAD"); after != before {
		t.Error("no-op save created a commit")
	}
}

// The whole point of --commit-email is that autosave commits carry an author
// email GitHub cannot link to an account, so they never reach the profile's
// contribution graph.
func TestInitCommitEmailKeepsCommitsOffTheProfile(t *testing.T) {
	e := newEnv(t)
	e.mustRun("init", "--root", e.root, "--workspace", "work", "--git",
		"--commit-email", "tuido@localhost")
	if got := strings.TrimSpace(gitOut(t, e.root, "config", "--local", "user.email")); got != "tuido@localhost" {
		t.Fatalf("repo-local user.email = %q, want tuido@localhost", got)
	}

	gitOut(t, e.root, "config", "user.name", "tuido") // commits also need a name
	e.write("work/data.md", "- [ ] typed on this machine\n")
	e.mustRun("_commit", filepath.Join(e.root, "work", "data.md"))
	if got := strings.TrimSpace(gitOut(t, e.root, "log", "-1", "--format=%ae")); got != "tuido@localhost" {
		t.Errorf("commit author email = %q, want tuido@localhost", got)
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
