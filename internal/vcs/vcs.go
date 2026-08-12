// Package vcs keeps the task repo in sync without ever blocking a command.
//
// Local commits are synchronous (no network, effectively instant). Fetching and
// pushing happen in a detached background process whose outcome is written to a
// state file and surfaced as a warning line by the next foreground command — a
// background job that fails silently is the classic failure mode, so nothing
// here retries in silence.
package vcs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// State is ~/.cache/tuido/sync.json.
type State struct {
	LastFetch int64  `json:"last_fetch"`
	LastPush  int64  `json:"last_push"`
	Unpushed  int    `json:"unpushed"`
	Behind    int    `json:"behind"`
	Diverged  bool   `json:"diverged"`
	LastError string `json:"last_error"`
}

// Repo is a task repo under git.
type Repo struct {
	Root     string
	CacheDir string
	Enabled  bool
	AutoPush bool

	remoteOnce bool
	remote     bool
}

// New returns a Repo. Enabled is false when the root is not a git work tree, so
// every call below degrades to a no-op rather than an error.
func New(root, cacheDir string, enabled, autoPush bool) *Repo {
	r := &Repo{Root: root, CacheDir: cacheDir, Enabled: enabled, AutoPush: autoPush}
	if enabled {
		if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
			r.Enabled = false
		}
	}
	return r
}

// HasRemote reports whether there is anywhere to sync to. A git repo with no
// remote is a perfectly good local-only setup — `tuido init` offers exactly
// that — so fetch and push become silent no-ops rather than a warning printed
// above every single command.
func (r *Repo) HasRemote() bool {
	if !r.Enabled {
		return false
	}
	if !r.remoteOnce {
		out, err := r.git("remote")
		r.remote = err == nil && strings.TrimSpace(out) != ""
		r.remoteOnce = true
	}
	return r.remote
}

// pushArgs sets the upstream on the first push, so a repo created by
// `tuido init --remote` against an empty remote publishes itself.
func (r *Repo) pushArgs(extra ...string) []string {
	args := append([]string{"push"}, extra...)
	if _, err := r.git("rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); err != nil {
		return append(args, "-u", "origin", "HEAD")
	}
	return args
}

func (r *Repo) statePath() string { return filepath.Join(r.CacheDir, "sync.json") }
func (r *Repo) logPath() string   { return filepath.Join(r.CacheDir, "sync.log") }
func (r *Repo) lockPath() string  { return filepath.Join(r.CacheDir, "git.lock") }

// ReadState returns the last known sync state. A missing or corrupt file is an
// empty state, not an error: sync status must never break a read command.
func (r *Repo) ReadState() State {
	var s State
	b, err := os.ReadFile(r.statePath())
	if err != nil {
		return s
	}
	_ = json.Unmarshal(b, &s)
	return s
}

func (r *Repo) writeState(s State) error {
	if err := os.MkdirAll(r.CacheDir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(r.statePath(), b, 0o644)
}

// Warning returns the one line a foreground command should print above its
// output, or "".
func (r *Repo) Warning() string {
	if !r.HasRemote() {
		return ""
	}
	s := r.ReadState()
	switch {
	case s.Diverged:
		return "sync: diverged from origin — run `tuido sync`"
	case s.LastError != "":
		return "sync: " + s.LastError + " — run `tuido sync`"
	case s.Unpushed > 0 && time.Since(time.Unix(s.LastPush, 0)) > 5*time.Minute:
		return fmt.Sprintf("sync: %d commit(s) not pushed — run `tuido sync`", s.Unpushed)
	}
	return ""
}

// MaybeFetch spawns a detached background sync if the last one is older than
// interval. It returns immediately; no command's latency depends on the
// network.
func (r *Repo) MaybeFetch(interval time.Duration) {
	if !r.HasRemote() {
		return
	}
	if time.Since(time.Unix(r.ReadState().LastFetch, 0)) < interval {
		return
	}
	self, err := os.Executable()
	if err != nil {
		return
	}
	if err := os.MkdirAll(r.CacheDir, 0o755); err != nil {
		return
	}
	logFile, err := os.OpenFile(r.logPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer logFile.Close()

	cmd := exec.Command(self, "internal-sync")
	cmd.Dir = r.Root
	cmd.Env = append(os.Environ(), "TUIDO_ROOT="+r.Root)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // survive our exit
	cmd.Stdout, cmd.Stderr = logFile, logFile
	_ = cmd.Start() // never Wait
}

// Background is the body of the hidden `internal-sync` command: fetch, then
// fast-forward only. It exits immediately if another process holds the lock.
func (r *Repo) Background() error {
	if !r.HasRemote() {
		return nil
	}
	unlock, ok, err := r.tryLock()
	if err != nil || !ok {
		return err
	}
	defer unlock()

	s := r.ReadState()
	s.LastFetch = time.Now().Unix()
	s.LastError = ""

	if out, err := r.git("fetch", "--quiet"); err != nil {
		s.LastError = firstLine(out)
		return r.writeState(s)
	}
	r.refreshCounts(&s)

	if s.Behind > 0 && s.Unpushed == 0 {
		if out, err := r.git("merge", "--ff-only", "--quiet"); err != nil {
			// Non-fast-forward means the branches diverged. Touch nothing; let
			// the next foreground command warn.
			s.Diverged = true
			s.LastError = firstLine(out)
			return r.writeState(s)
		}
		r.refreshCounts(&s)
	}

	if r.AutoPush && s.Unpushed > 0 && !s.Diverged {
		if out, err := r.git(r.pushArgs("--quiet")...); err != nil {
			s.LastError = firstLine(out)
		} else {
			s.LastPush = time.Now().Unix()
			r.refreshCounts(&s)
		}
	}
	return r.writeState(s)
}

// Commit stages the given files and commits them locally, in one commit.
// Synchronous and offline, so it costs nothing measurable; the granular
// messages make `git log` a usable activity record for free.
func (r *Repo) Commit(message string, paths ...string) error {
	if !r.Enabled || len(paths) == 0 {
		return nil
	}
	unlock, err := r.lock()
	if err != nil {
		return err
	}
	defer unlock()

	rels := make([]string, len(paths))
	for i, p := range paths {
		rel, err := filepath.Rel(r.Root, p)
		if err != nil {
			rel = p
		}
		rels[i] = rel
	}
	if out, err := r.git(append([]string{"add", "--"}, rels...)...); err != nil {
		return fmt.Errorf("git add: %s", firstLine(out))
	}
	if out, err := r.git(append([]string{"diff", "--cached", "--quiet", "--"}, rels...)...); err == nil && out == "" {
		return nil // nothing staged; not an error
	}
	if out, err := r.git(append([]string{"commit", "--quiet", "--only", "-m", message, "--"}, rels...)...); err != nil {
		return fmt.Errorf("git commit: %s", firstLine(out))
	}

	s := r.ReadState()
	r.refreshCounts(&s)
	_ = r.writeState(s)
	return nil
}

// PushAsync spawns a detached push and forgets about it.
func (r *Repo) PushAsync() {
	if !r.HasRemote() || !r.AutoPush {
		return
	}
	self, err := os.Executable()
	if err != nil {
		return
	}
	logFile, err := os.OpenFile(r.logPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer logFile.Close()

	cmd := exec.Command(self, "internal-sync", "--push")
	cmd.Dir = r.Root
	cmd.Env = append(os.Environ(), "TUIDO_ROOT="+r.Root)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	_ = cmd.Start()
}

// Push is the background push half, used by `internal-sync --push`.
func (r *Repo) Push() error {
	if !r.HasRemote() {
		return nil
	}
	unlock, ok, err := r.tryLock()
	if err != nil || !ok {
		return err
	}
	defer unlock()

	s := r.ReadState()
	if out, err := r.git(r.pushArgs("--quiet")...); err != nil {
		s.LastError = firstLine(out)
	} else {
		s.LastPush = time.Now().Unix()
		s.LastError = ""
	}
	r.refreshCounts(&s)
	return r.writeState(s)
}

// SyncResult is what `tuido sync` reports.
type SyncResult struct {
	Fetched  bool
	Rebased  bool
	Pushed   int
	Conflict bool
	Messages []string
}

// Sync is the blocking, verbose, on-demand version: fetch, rebase, push.
func (r *Repo) Sync() (SyncResult, error) {
	var res SyncResult
	if !r.Enabled {
		return res, errors.New("git is not enabled for this root")
	}
	if !r.HasRemote() {
		// Local-only is a supported setup, not a failure.
		res.Messages = append(res.Messages, "no remote configured — commits stay local")
		return res, nil
	}
	unlock, err := r.lock()
	if err != nil {
		return res, err
	}
	defer unlock()

	s := r.ReadState()
	s.LastError = ""

	if out, err := r.git("fetch"); err != nil {
		return res, fmt.Errorf("fetch: %s", firstLine(out))
	}
	res.Fetched = true
	s.LastFetch = time.Now().Unix()
	r.refreshCounts(&s)

	if s.Behind > 0 {
		if out, err := r.git("rebase", "--quiet"); err != nil {
			// Leave nothing half-rebased: abort, then tell the user where to
			// look. tuido never resolves a content conflict on its own.
			_, _ = r.git("rebase", "--abort")
			res.Conflict = true
			s.Diverged = true
			s.LastError = conflictLine(out)
			_ = r.writeState(s)
			return res, fmt.Errorf("rebase stopped: %s\n"+
				"  resolve it in your editor: cd %s && git pull --rebase", conflictLine(out), r.Root)
		}
		res.Rebased = true
	}
	s.Diverged = false
	r.refreshCounts(&s)

	if s.Unpushed > 0 {
		n := s.Unpushed
		if out, err := r.git(r.pushArgs()...); err != nil {
			s.LastError = firstLine(out)
			_ = r.writeState(s)
			return res, fmt.Errorf("push: %s", firstLine(out))
		}
		res.Pushed = n
		s.LastPush = time.Now().Unix()
	}
	r.refreshCounts(&s)
	return res, r.writeState(s)
}

// refreshCounts updates ahead/behind. A repo with no upstream simply has no
// counts; that is not an error.
func (r *Repo) refreshCounts(s *State) {
	out, err := r.git("rev-list", "--left-right", "--count", "HEAD...@{upstream}")
	if err != nil {
		s.Unpushed, s.Behind = 0, 0
		return
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return
	}
	s.Unpushed, _ = strconv.Atoi(fields[0])
	s.Behind, _ = strconv.Atoi(fields[1])
	if s.Unpushed > 0 && s.Behind > 0 {
		s.Diverged = true
	} else if s.Behind == 0 {
		s.Diverged = false
	}
}

func (r *Repo) git(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Root
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// lock blocks until the repo lock is available. Mutating commands and the
// background merge take the same lock, so a merge can never race a write.
func (r *Repo) lock() (func(), error) {
	f, err := r.lockFile()
	if err != nil {
		return func() {}, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return func() {}, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// tryLock takes the lock if it is free. Background work that cannot get it
// exits immediately rather than queueing.
func (r *Repo) tryLock() (func(), bool, error) {
	f, err := r.lockFile()
	if err != nil {
		return func() {}, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return func() {}, false, nil
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, true, nil
}

func (r *Repo) lockFile() (*os.File, error) {
	if err := os.MkdirAll(r.CacheDir, 0o755); err != nil {
		return nil, err
	}
	return os.OpenFile(r.lockPath(), os.O_CREATE|os.O_RDWR, 0o644)
}

// conflictLine prefers git's CONFLICT line, which names the file, over the
// "Auto-merging …" chatter that precedes it.
func conflictLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "CONFLICT") {
			return strings.TrimSpace(line)
		}
	}
	return firstLine(s)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}
