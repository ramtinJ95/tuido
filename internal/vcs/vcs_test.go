package vcs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL="+filepath.Join(t.TempDir(), "gitconfig"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=tuido", "GIT_AUTHOR_EMAIL=tuido@example.com",
		"GIT_COMMITTER_NAME=tuido", "GIT_COMMITTER_EMAIL=tuido@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// testRepos builds a bare origin and two clones of it, which is the two-machine
// situation the whole design exists to handle.
func testRepos(t *testing.T) (originDir, aDir, bDir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	originDir = filepath.Join(base, "origin.git")
	git(t, base, "init", "--bare", "--initial-branch=main", originDir)

	aDir = filepath.Join(base, "a")
	git(t, base, "clone", "--quiet", originDir, aDir)
	configure(t, aDir)

	// A first commit, so the clone below has a branch to track.
	write(t, filepath.Join(aDir, "work", "inbox.md"), "- [ ] first\n")
	git(t, aDir, "add", "-A")
	git(t, aDir, "commit", "--quiet", "-m", "initial")
	git(t, aDir, "push", "--quiet", "-u", "origin", "main")

	bDir = filepath.Join(base, "b")
	git(t, base, "clone", "--quiet", originDir, bDir)
	configure(t, bDir)
	return originDir, aDir, bDir
}

func configure(t *testing.T, dir string) {
	t.Helper()
	git(t, dir, "config", "user.name", "tuido")
	git(t, dir, "config", "user.email", "tuido@example.com")
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func repoFor(t *testing.T, dir string) *Repo {
	t.Helper()
	return New(dir, filepath.Join(t.TempDir(), "cache"), true, true)
}

func TestCommitThenPushThenFastForward(t *testing.T) {
	_, aDir, bDir := testRepos(t)
	a, b := repoFor(t, aDir), repoFor(t, bDir)

	p := filepath.Join(aDir, "work", "inbox.md")
	write(t, p, "- [ ] first\n- [ ] second\n")
	if err := a.Commit(p, "add: second (work/inbox)"); err != nil {
		t.Fatal(err)
	}
	if s := a.ReadState(); s.Unpushed != 1 {
		t.Errorf("unpushed = %d, want 1", s.Unpushed)
	}
	if err := a.Push(); err != nil {
		t.Fatal(err)
	}
	if s := a.ReadState(); s.Unpushed != 0 {
		t.Errorf("unpushed after push = %d, want 0", s.Unpushed)
	}

	// b picks it up with a plain fast-forward and no user involvement.
	if err := b.Background(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(bDir, "work", "inbox.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "second") {
		t.Errorf("b did not fast-forward:\n%s", body)
	}
	if s := b.ReadState(); s.Behind != 0 || s.Diverged {
		t.Errorf("state after ff = %+v", s)
	}
	if w := b.Warning(); w != "" {
		t.Errorf("warning after a clean sync: %q", w)
	}
}

// Divergence must be detected and reported, never resolved by guessing.
func TestDivergenceIsDetectedAndWarned(t *testing.T) {
	_, aDir, bDir := testRepos(t)
	a, b := repoFor(t, aDir), repoFor(t, bDir)

	pa := filepath.Join(aDir, "work", "inbox.md")
	write(t, pa, "- [ ] first\n- [ ] from a\n")
	if err := a.Commit(pa, "add: from a"); err != nil {
		t.Fatal(err)
	}
	if err := a.Push(); err != nil {
		t.Fatal(err)
	}

	// b edits a different file, so the branches diverge without the contents
	// actually conflicting.
	pb := filepath.Join(bDir, "work", "personal.md")
	write(t, pb, "- [ ] from b\n")
	if err := b.Commit(pb, "add: from b"); err != nil {
		t.Fatal(err)
	}

	if err := b.Background(); err != nil {
		t.Fatal(err)
	}
	s := b.ReadState()
	if !s.Diverged {
		t.Fatalf("divergence not detected: %+v", s)
	}
	if w := b.Warning(); !strings.Contains(w, "diverged") {
		t.Errorf("warning = %q", w)
	}
	// The background job must not have touched the work tree.
	body, _ := os.ReadFile(filepath.Join(bDir, "work", "inbox.md"))
	if strings.Contains(string(body), "from a") {
		t.Errorf("background job merged a diverged work tree:\n%s", body)
	}

	// The blocking, on-demand version rebases and pushes.
	if _, err := b.Sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	body, _ = os.ReadFile(pb)
	if !strings.Contains(string(body), "from b") {
		t.Errorf("rebase lost the local change:\n%s", body)
	}
	if s := b.ReadState(); s.Diverged || s.Unpushed != 0 {
		t.Errorf("state after sync = %+v", s)
	}
}

// A real content conflict stops the sync, aborts the rebase cleanly and points
// the user at their editor. tuido never resolves one on its own.
func TestSyncStopsOnContentConflict(t *testing.T) {
	_, aDir, bDir := testRepos(t)
	a, b := repoFor(t, aDir), repoFor(t, bDir)

	pa := filepath.Join(aDir, "work", "inbox.md")
	write(t, pa, "- [ ] first\n- [ ] from a\n")
	if err := a.Commit(pa, "add: from a"); err != nil {
		t.Fatal(err)
	}
	if err := a.Push(); err != nil {
		t.Fatal(err)
	}

	pb := filepath.Join(bDir, "work", "inbox.md")
	write(t, pb, "- [ ] first\n- [ ] from b\n")
	if err := b.Commit(pb, "add: from b"); err != nil {
		t.Fatal(err)
	}

	res, err := b.Sync()
	if err == nil {
		t.Fatal("sync did not report the conflict")
	}
	if !res.Conflict {
		t.Error("result does not record a conflict")
	}
	if !strings.Contains(err.Error(), "work/inbox.md") {
		t.Errorf("error does not name the file: %v", err)
	}

	// The rebase was aborted, so the work tree is usable and holds b's version.
	body, _ := os.ReadFile(pb)
	if !strings.Contains(string(body), "from b") {
		t.Errorf("local change lost:\n%s", body)
	}
	if strings.Contains(string(body), "<<<<<<<") {
		t.Errorf("conflict markers left in the work tree:\n%s", body)
	}
	if state := git(t, bDir, "status", "--porcelain"); state != "" {
		t.Errorf("work tree not clean after abort:\n%s", state)
	}
}

// A foreground write and a background merge take the same lock, so they cannot
// race.
func TestLockContention(t *testing.T) {
	_, aDir, _ := testRepos(t)
	cache := filepath.Join(t.TempDir(), "cache")
	a := New(aDir, cache, true, true)
	other := New(aDir, cache, true, true)

	unlock, err := a.lock()
	if err != nil {
		t.Fatal(err)
	}
	release, ok, err := other.tryLock()
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		release()
		t.Fatal("tryLock succeeded while the lock was held")
	}
	unlock()

	release, ok, err = other.tryLock()
	if err != nil || !ok {
		t.Fatalf("tryLock after release: ok=%v err=%v", ok, err)
	}
	release()
}

// Everything degrades to a no-op when the root is not a git work tree, rather
// than failing a read command.
func TestDisabledOutsideAGitRepo(t *testing.T) {
	dir := t.TempDir()
	r := New(dir, filepath.Join(dir, "cache"), true, true)
	if r.Enabled {
		t.Fatal("enabled outside a git repo")
	}
	if err := r.Commit(filepath.Join(dir, "x.md"), "msg"); err != nil {
		t.Errorf("commit: %v", err)
	}
	if err := r.Background(); err != nil {
		t.Errorf("background: %v", err)
	}
	if w := r.Warning(); w != "" {
		t.Errorf("warning = %q", w)
	}
	if _, err := r.Sync(); err == nil {
		t.Error("sync should say git is not enabled")
	}
}

// A git repo with no remote is a supported local-only setup. It must commit
// happily and never print a warning above every command.
func TestLocalOnlyRepoIsSilent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	git(t, dir, "init", "--quiet", "--initial-branch=main", dir)
	configure(t, dir)

	r := repoFor(t, dir)
	if !r.Enabled {
		t.Fatal("repo not detected as a git work tree")
	}
	if r.HasRemote() {
		t.Fatal("HasRemote true with no remote configured")
	}

	p := filepath.Join(dir, "work", "inbox.md")
	write(t, p, "- [ ] local only\n")
	if err := r.Commit(p, "add: local only"); err != nil {
		t.Fatal(err)
	}
	if out := git(t, dir, "log", "--oneline"); !strings.Contains(out, "add: local only") {
		t.Errorf("commit missing from log: %q", out)
	}
	if w := r.Warning(); w != "" {
		t.Errorf("warning on a local-only repo: %q", w)
	}
	if err := r.Background(); err != nil {
		t.Errorf("background: %v", err)
	}
	res, err := r.Sync()
	if err != nil {
		t.Errorf("sync on a local-only repo errored: %v", err)
	}
	if len(res.Messages) == 0 || !strings.Contains(res.Messages[0], "no remote") {
		t.Errorf("sync messages = %v", res.Messages)
	}
}

// The first push must set the upstream, or a repo created by
// `tuido init --remote` against an empty remote never publishes itself.
func TestFirstPushSetsUpstream(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	origin := filepath.Join(base, "origin.git")
	git(t, base, "init", "--bare", "--initial-branch=main", origin)

	work := filepath.Join(base, "work")
	git(t, base, "init", "--quiet", "--initial-branch=main", work)
	configure(t, work)
	git(t, work, "remote", "add", "origin", origin)

	r := repoFor(t, work)
	p := filepath.Join(work, "work", "inbox.md")
	write(t, p, "- [ ] first ever\n")
	if err := r.Commit(p, "add: first ever"); err != nil {
		t.Fatal(err)
	}
	if err := r.Push(); err != nil {
		t.Fatal(err)
	}
	if out := git(t, base, "--git-dir", origin, "log", "--oneline"); !strings.Contains(out, "first ever") {
		t.Errorf("origin did not receive the first push: %q", out)
	}
}

func TestCommitWithNothingStagedIsNotAnError(t *testing.T) {
	_, aDir, _ := testRepos(t)
	a := repoFor(t, aDir)
	p := filepath.Join(aDir, "work", "inbox.md")
	if err := a.Commit(p, "no change"); err != nil {
		t.Errorf("commit with no change: %v", err)
	}
}
