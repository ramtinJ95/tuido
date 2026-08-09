package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// The test binary doubles as the tuido binary: with TUIDO_TEST_SUBPROCESS set
// it dispatches instead of running tests. That gives real process boundaries
// and real exit codes without a second build step.
func TestMain(m *testing.M) {
	if os.Getenv("TUIDO_TEST_SUBPROCESS") != "" {
		os.Exit(run(os.Args[1:]))
	}
	os.Exit(m.Run())
}

type env struct {
	t    *testing.T
	home string
	root string
}

// newEnv points XDG at a temp dir so nothing touches the real config.
func newEnv(t *testing.T) *env {
	t.Helper()
	home := t.TempDir()
	return &env{t: t, home: home, root: filepath.Join(home, "todo")}
}

type result struct {
	stdout, stderr string
	code           int
}

func (e *env) run(args ...string) result {
	e.t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(),
		"TUIDO_TEST_SUBPROCESS=1",
		"XDG_CONFIG_HOME="+filepath.Join(e.home, "config"),
		"XDG_STATE_HOME="+filepath.Join(e.home, "state"),
		"XDG_CACHE_HOME="+filepath.Join(e.home, "cache"),
		"NO_COLOR=1",
		"TUIDO_WORKSPACE=",
	)
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		e.t.Fatalf("running %v: %v", args, err)
	}
	return result{out.String(), errb.String(), code}
}

// mustRun fails the test if the command does not exit 0.
func (e *env) mustRun(args ...string) result {
	e.t.Helper()
	r := e.run(args...)
	if r.code != 0 {
		e.t.Fatalf("tuido %s exited %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), r.code, r.stdout, r.stderr)
	}
	return r
}

func (e *env) init() {
	e.t.Helper()
	e.mustRun("init", "--root", e.root, "--workspace", "work", "--no-git")
}

func (e *env) read(rel string) string {
	e.t.Helper()
	b, err := os.ReadFile(filepath.Join(e.root, rel))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

func (e *env) write(rel, body string) {
	e.t.Helper()
	p := filepath.Join(e.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		e.t.Fatal(err)
	}
}

// A fresh machine goes from nothing to a rendered `ls` with no hand-written
// config. This is the M1 bar.
func TestInitThenCaptureThenList(t *testing.T) {
	e := newEnv(t)
	e.init()

	if _, err := os.Stat(filepath.Join(e.home, "config", "tuido", "config.toml")); err != nil {
		t.Fatalf("config not written: %v", err)
	}
	e.mustRun("add", "rotate vault certs", "-p", "high", "-d", "2026-09-01")
	e.mustRun("add", "-p", "highest", "audit IAM policies", "-t", "security")

	body := e.read("work/inbox.md")
	if !strings.Contains(body, "- [ ] rotate vault certs ⏫ 📅 2026-09-01 ➕") {
		t.Errorf("unexpected capture:\n%s", body)
	}
	if !strings.Contains(body, "#security 🔺") {
		t.Errorf("tag or priority missing:\n%s", body)
	}

	out := e.mustRun("ls").stdout
	for _, want := range []string{"work/inbox", "rotate vault certs", "audit IAM policies"} {
		if !strings.Contains(out, want) {
			t.Errorf("ls output missing %q:\n%s", want, out)
		}
	}
}

// Flags after the positional text is the whole reason pflag is here.
func TestFlagsAfterText(t *testing.T) {
	e := newEnv(t)
	e.init()
	e.mustRun("add", "rotate", "vault", "certs", "-p", "high")
	if body := e.read("work/inbox.md"); !strings.Contains(body, "- [ ] rotate vault certs ⏫") {
		t.Errorf("trailing flag not applied:\n%s", body)
	}
}

func TestInitIsIdempotentAndForceOverwrites(t *testing.T) {
	e := newEnv(t)
	e.init()
	again := e.mustRun("init", "--root", "/somewhere/else", "--no-git")
	if !strings.Contains(again.stdout, "already initialised") {
		t.Errorf("re-init did not report idempotence: %q", again.stdout)
	}
	cfg, err := os.ReadFile(filepath.Join(e.home, "config", "tuido", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(cfg), "somewhere/else") {
		t.Error("re-init overwrote the config without --force")
	}

	other := filepath.Join(e.home, "todo2")
	e.mustRun("init", "--root", other, "--no-git", "--force")
	cfg2, _ := os.ReadFile(filepath.Join(e.home, "config", "tuido", "config.toml"))
	if !strings.Contains(string(cfg2), "todo2") {
		t.Errorf("--force did not overwrite:\n%s", cfg2)
	}
}

// No config and no terminal to ask in: exit 4 with instructions, never a guess.
func TestNotInitialisedExits4(t *testing.T) {
	e := newEnv(t)
	r := e.run("ls")
	if r.code != 4 {
		t.Fatalf("exit = %d, want 4\nstderr: %s", r.code, r.stderr)
	}
	if !strings.Contains(r.stderr, "tuido init") {
		t.Errorf("stderr does not name the fix: %q", r.stderr)
	}
}

func TestInitRequiresRootWhenNotATerminal(t *testing.T) {
	e := newEnv(t)
	r := e.run("init", "--no-git")
	if r.code != 1 {
		t.Fatalf("exit = %d, want 1\nstderr: %s", r.code, r.stderr)
	}
	if !strings.Contains(r.stderr, "--root is required") {
		t.Errorf("stderr = %q", r.stderr)
	}
}

// Capture into a structured file refuses and lists the sections, rather than
// burying the task under whatever heading happens to be last.
func TestStructuredCaptureRefusal(t *testing.T) {
	e := newEnv(t)
	e.init()
	e.write("work/oncall.md", "# now\n\n- [ ] existing\n\n# next\n\n- [ ] later\n")

	r := e.run("add", "new task", "-l", "oncall")
	if r.code != 1 {
		t.Fatalf("exit = %d, want 1\nstderr: %s", r.code, r.stderr)
	}
	for _, want := range []string{"needs a section", "now", "next"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("refusal does not mention %q:\n%s", want, r.stderr)
		}
	}

	// Naming the section works, and the task lands in that section's block.
	e.mustRun("add", "new task", "-l", "oncall/now")
	body := e.read("work/oncall.md")
	nowIdx := strings.Index(body, "- [ ] new task")
	nextIdx := strings.Index(body, "# next")
	if nowIdx < 0 || nowIdx > nextIdx {
		t.Errorf("task did not land under `now`:\n%s", body)
	}

	// A marker comment supplies the default, so -l alone works afterwards.
	e.write("work/triage.md", "<!-- tuido: capture=inbox -->\n\n# inbox\n\n- [ ] a\n\n# done\n")
	e.mustRun("add", "via marker", "-l", "triage")
	if body := e.read("work/triage.md"); !strings.Contains(body, "- [ ] a\n- [ ] via marker") {
		t.Errorf("marker capture did not append to the inbox block:\n%s", body)
	}
}

func TestNoMatchAndAmbiguousAreExit1(t *testing.T) {
	e := newEnv(t)
	e.init()
	e.mustRun("add", "rotate vault certs")
	e.mustRun("add", "rotate database certs")

	r := e.run("done", "nonexistent")
	if r.code != 1 || !strings.Contains(r.stderr, "no task matches") {
		t.Errorf("no-match: exit %d, stderr %q", r.code, r.stderr)
	}

	// Ambiguous with no terminal prints the candidates and exits 1, which is
	// what keeps the command scriptable.
	r = e.run("done", "rotate")
	if r.code != 1 {
		t.Fatalf("ambiguous: exit = %d, want 1", r.code)
	}
	for _, want := range []string{"matches 2 tasks", "rotate vault certs", "rotate database certs"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("ambiguity report missing %q:\n%s", want, r.stderr)
		}
	}
	if strings.Contains(e.read("work/inbox.md"), "[x]") {
		t.Error("an ambiguous query completed a task anyway")
	}
}

// A conflicted file must never be parsed, let alone sorted.
func TestConflictedFileExits3(t *testing.T) {
	e := newEnv(t)
	e.init()
	e.write("work/oncall.md", "# now\n\n<<<<<<< HEAD\n- [ ] mine\n=======\n- [ ] theirs\n>>>>>>> origin/main\n")

	r := e.run("sort", "oncall")
	if r.code != 3 {
		t.Fatalf("exit = %d, want 3\nstderr: %s", r.code, r.stderr)
	}
	if !strings.Contains(r.stderr, "conflict") {
		t.Errorf("stderr = %q", r.stderr)
	}
	if !strings.Contains(e.read("work/oncall.md"), "<<<<<<< HEAD") {
		t.Error("the conflicted file was rewritten")
	}
}

func TestSortIsIdempotentThroughTheCLI(t *testing.T) {
	e := newEnv(t)
	e.init()
	e.write("work/oncall.md", "- [ ] zebra ⏬\n- [ ] apple 🔺\n- [ ] middle 🔼\n")

	e.mustRun("sort", "oncall")
	once := e.read("work/oncall.md")
	if !strings.HasPrefix(once, "- [ ] apple 🔺\n") {
		t.Fatalf("not sorted:\n%s", once)
	}
	e.mustRun("sort", "oncall")
	if twice := e.read("work/oncall.md"); twice != once {
		t.Errorf("sort is not idempotent:\n%s\n---\n%s", once, twice)
	}
}

func TestSortRespectsSortNone(t *testing.T) {
	e := newEnv(t)
	e.init()
	src := "<!-- tuido: sort=none -->\n- [ ] zebra ⏬\n- [ ] apple 🔺\n"
	e.write("work/runbook.md", src)

	e.mustRun("sort", "runbook")
	if e.read("work/runbook.md") != src {
		t.Error("sort=none file was reordered")
	}
	e.mustRun("sort", "runbook", "--by", "prio")
	if e.read("work/runbook.md") == src {
		t.Error("explicit --by did not override sort=none")
	}
}

// Styling must be absent, not empty escape sequences, when the output is not a
// terminal.
func TestOutputIsPlainWithoutATerminal(t *testing.T) {
	e := newEnv(t)
	e.init()
	e.mustRun("add", "plain output", "-p", "highest")
	out := e.mustRun("ls").stdout
	if strings.Contains(out, "\x1b[") {
		t.Errorf("escape sequences in non-TTY output: %q", out)
	}
}

func TestDoneStampsAndDoesNotMoveTheLine(t *testing.T) {
	e := newEnv(t)
	e.init()
	e.write("work/oncall.md", "- [ ] first\n- [ ] second\n- [ ] third\n")

	e.mustRun("done", "second")
	body := e.read("work/oncall.md")
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("line count changed:\n%s", body)
	}
	if !strings.HasPrefix(lines[1], "- [x] second ✅ ") {
		t.Errorf("line 2 = %q", lines[1])
	}
	if lines[0] != "- [ ] first" || lines[2] != "- [ ] third" {
		t.Errorf("neighbouring lines were touched:\n%s", body)
	}
}

// A mutation must rewrite exactly the line it changed, so git diffs stay the
// size of the actual change.
func TestMutationTouchesOneLineOnly(t *testing.T) {
	e := newEnv(t)
	e.init()
	src := "# now\n\n" +
		"- [ ] hand written ➕ 2026-01-01 ⏫ 📅 2026-02-02\n" +
		"* [ ] star bullet with trailing space   \n" +
		"- [ ] target\n"
	e.write("work/oncall.md", src)

	e.mustRun("done", "target")
	before := strings.Split(src, "\n")
	after := strings.Split(e.read("work/oncall.md"), "\n")
	changed := 0
	for i := range before {
		if i < len(after) && before[i] != after[i] {
			changed++
		}
	}
	if changed != 1 {
		t.Errorf("%d lines changed, want 1:\n%s", changed, strings.Join(after, "\n"))
	}
}

func TestUseReportsSourceAndRejectsUnknown(t *testing.T) {
	e := newEnv(t)
	e.init()
	if out := e.mustRun("use").stdout; !strings.Contains(out, "work") || !strings.Contains(out, "from") {
		t.Errorf("use = %q", out)
	}
	r := e.run("use", "nope")
	if r.code != 1 || !strings.Contains(r.stderr, "no workspace") {
		t.Errorf("exit %d, stderr %q", r.code, r.stderr)
	}
}

func TestPathAndUnknownCommand(t *testing.T) {
	e := newEnv(t)
	e.init()
	out := strings.TrimSpace(e.mustRun("path", "inbox").stdout)
	if out != filepath.Join(e.root, "work", "inbox.md") {
		t.Errorf("path = %q", out)
	}
	if r := e.run("frobnicate"); r.code != 1 {
		t.Errorf("unknown command exit = %d, want 1", r.code)
	}
}

// Setting up a second machine is the case the whole sync design exists for:
// --remote pointing at a repo that already has commits must clone it, not
// initialise an empty one beside it and immediately diverge.
func TestInitRemoteAdoptsAnExistingRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	first := newEnv(t)
	first.init()
	first.write("work/inbox.md", "- [ ] from the first machine\n")

	// Publish it to a bare origin.
	base := t.TempDir()
	origin := filepath.Join(base, "origin.git")
	gitcfg := filepath.Join(base, "gitconfig")
	rungit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL="+gitcfg, "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	rungit(base, "init", "--bare", "--initial-branch=main", origin)
	rungit(base, "init", "--quiet", "--initial-branch=main", first.root)
	rungit(first.root, "add", "-A")
	rungit(first.root, "commit", "--quiet", "-m", "initial")
	rungit(first.root, "remote", "add", "origin", origin)
	rungit(first.root, "push", "--quiet", "-u", "origin", "HEAD")

	second := newEnv(t)
	r := second.run("init", "--root", second.root, "--remote", origin)
	if r.code != 0 {
		t.Fatalf("init --remote exited %d\nstdout: %s\nstderr: %s", r.code, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stdout, "cloning") {
		t.Errorf("did not clone:\n%s", r.stdout)
	}
	if body := second.read("work/inbox.md"); !strings.Contains(body, "from the first machine") {
		t.Errorf("clone did not bring the tasks:\n%s", body)
	}
	// The workspace came with the clone, so ls works with no further setup.
	if out := second.mustRun("ls").stdout; !strings.Contains(out, "from the first machine") {
		t.Errorf("ls after clone:\n%s", out)
	}

	// And it refuses to clone over existing content.
	third := newEnv(t)
	occupied := filepath.Join(third.home, "occupied")
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(occupied, "junk"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = third.run("init", "--root", occupied, "--remote", origin)
	if r.code != 1 || !strings.Contains(r.stderr, "not empty") {
		t.Errorf("clobber refusal: exit %d, stderr %q", r.code, r.stderr)
	}
}

// Help must be discoverable and must exit 0. A non-zero exit here tells any
// caller probing the CLI that the command is broken.
func TestHelpIsDiscoverableAndExitsZero(t *testing.T) {
	e := newEnv(t)

	for _, args := range [][]string{{"-h"}, {"--help"}, {"help"}} {
		r := e.run(args...)
		if r.code != 0 {
			t.Errorf("tuido %v exited %d, want 0", args, r.code)
		}
		if !strings.Contains(r.stdout, "tuido add") || !strings.Contains(r.stdout, "Exit codes:") {
			t.Errorf("tuido %v did not list the commands:\n%s", args, r.stdout)
		}
		if !strings.Contains(r.stdout, "--help") {
			t.Errorf("tuido %v does not point at per-command help:\n%s", args, r.stdout)
		}
	}

	// Every documented command answers both help forms, without needing config.
	for _, name := range commandNames() {
		for _, args := range [][]string{{name, "--help"}, {name, "-h"}, {"help", name}} {
			r := e.run(args...)
			if r.code != 0 {
				t.Errorf("tuido %v exited %d, want 0\nstderr: %s", args, r.code, r.stderr)
				continue
			}
			if r.stderr != "" {
				t.Errorf("tuido %v wrote to stderr: %q", args, r.stderr)
			}
			for _, want := range []string{"tuido " + name, "Usage:", "Exit codes:"} {
				if !strings.Contains(r.stdout, want) {
					t.Errorf("tuido %v help missing %q:\n%s", args, want, r.stdout)
				}
			}
		}
	}
}

// The help text is the CLI's contract. If a flag exists it must be documented,
// and if help claims a command exists it must be dispatchable.
func TestHelpMatchesTheRealFlags(t *testing.T) {
	e := newEnv(t)
	for _, name := range commandNames() {
		out := e.mustRun(name, "--help").stdout

		fs := newFlagSet(name)
		registerFlags(name, fs)
		fs.VisitAll(func(f *pflag.Flag) {
			if !strings.Contains(out, "--"+f.Name) {
				t.Errorf("tuido %s --help omits --%s", name, f.Name)
			}
		})

		if !strings.Contains(out, "Examples:") {
			t.Errorf("tuido %s --help has no examples", name)
		}
		// An unknown command must not be reachable from the help listing.
		if r := e.run(name, "--definitely-not-a-flag"); r.code != 1 {
			t.Errorf("tuido %s with a bad flag exited %d, want 1 (user error)", name, r.code)
		}
	}
}

func TestHelpForUnknownCommand(t *testing.T) {
	e := newEnv(t)
	r := e.run("help", "frobnicate")
	if r.code != 1 {
		t.Errorf("exit = %d, want 1", r.code)
	}
	if !strings.Contains(r.stderr, "unknown command") {
		t.Errorf("stderr = %q", r.stderr)
	}
}

func TestReleaseVersionRecognition(t *testing.T) {
	cases := map[string]bool{
		"v0.2.0":           true,
		"v0.2.0-rc1":       true,
		"v0.1.1+dirty":     false,
		"v0.1.1-1-gabc123": false,
		"v0.1.1-dirty":     false,
		"dev":              false,
		"dev+abc123":       false,
	}
	for version, want := range cases {
		if got := isReleaseVersion(version); got != want {
			t.Errorf("isReleaseVersion(%q) = %v, want %v", version, got, want)
		}
	}
}

func TestAddFromStdin(t *testing.T) {
	e := newEnv(t)
	e.init()
	cmd := exec.Command(os.Args[0], "add", "-")
	cmd.Env = append(os.Environ(),
		"TUIDO_TEST_SUBPROCESS=1",
		"XDG_CONFIG_HOME="+filepath.Join(e.home, "config"),
		"XDG_STATE_HOME="+filepath.Join(e.home, "state"),
		"XDG_CACHE_HOME="+filepath.Join(e.home, "cache"),
		"NO_COLOR=1",
	)
	cmd.Stdin = strings.NewReader("first from stdin\n\nsecond from stdin\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	body := e.read("work/inbox.md")
	if !strings.Contains(body, "first from stdin") || !strings.Contains(body, "second from stdin") {
		t.Errorf("stdin capture:\n%s", body)
	}
}

// `ls --json` is the machine-readable interface: the visibility rules match
// the human output, hidden tasks carry the reason, and stdout is pure JSON.
func TestLsJSON(t *testing.T) {
	e := newEnv(t)
	e.init()
	e.write("work/inbox.md",
		"- [ ] rotate vault certs ⏫ 📅 2026-09-01 #infra\n"+
			"- [x] draft the runbook ✅ 2026-08-05\n")

	var tasks []map[string]any
	out := e.mustRun("ls", "--json").stdout
	if err := json.Unmarshal([]byte(out), &tasks); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, out)
	}
	if len(tasks) != 1 {
		t.Fatalf("want 1 actionable task, got %d:\n%s", len(tasks), out)
	}
	got := tasks[0]
	for k, want := range map[string]string{
		"workspace": "work",
		"list":      "inbox",
		"state":     "open",
		"desc":      "rotate vault certs #infra",
		"priority":  "high",
		"due":       "2026-09-01",
	} {
		if got[k] != want {
			t.Errorf("%s = %v, want %q", k, got[k], want)
		}
	}
	if got["line"] != float64(1) {
		t.Errorf("line = %v, want 1", got["line"])
	}
	if got["path"] != filepath.Join(e.root, "work", "inbox.md") {
		t.Errorf("path = %v", got["path"])
	}

	out = e.mustRun("ls", "--json", "--all").stdout
	if err := json.Unmarshal([]byte(out), &tasks); err != nil {
		t.Fatalf("--all stdout is not valid JSON: %v\n%s", err, out)
	}
	if len(tasks) != 2 {
		t.Fatalf("want 2 tasks with --all, got %d:\n%s", len(tasks), out)
	}
	var done map[string]any
	for _, task := range tasks {
		if task["state"] == "done" {
			done = task
		}
	}
	if done == nil {
		t.Fatalf("done task missing from --all output:\n%s", out)
	}
	if done["hidden"] != "done" || done["completed"] != "2026-08-05" {
		t.Errorf("done task fields: hidden = %v, completed = %v", done["hidden"], done["completed"])
	}
}

// An empty result is an empty array, so `| jq '.[]'` never chokes on null.
func TestLsJSONEmptyIsArray(t *testing.T) {
	e := newEnv(t)
	e.init()
	out := e.mustRun("ls", "--json").stdout
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("empty ls --json = %q, want []", out)
	}
}

// The briefing is documentation: it prints before init (exit 0, naming the
// fix) and carries this machine's real root once initialised.
func TestAgentsBriefing(t *testing.T) {
	e := newEnv(t)
	pre := e.mustRun("agents")
	if !strings.Contains(pre.stdout, "tuido init") {
		t.Errorf("uninitialised briefing does not name the fix:\n%s", pre.stdout)
	}

	e.init()
	out := e.mustRun("agents").stdout
	for _, want := range []string{e.root, "current: work", "--json", "tuido done", "tuido fmt", "Exit codes"} {
		if !strings.Contains(out, want) {
			t.Errorf("briefing missing %q", want)
		}
	}
}
