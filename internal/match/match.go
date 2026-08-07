// Package match resolves a fuzzy text query to a single task.
//
// There is no index and no id cache, so a query can never go stale: an edit
// made in nvim is visible to the very next command. Ambiguity is resolved by a
// picker or an error, never by a silently-chosen default — completing the wrong
// task is worse than refusing.
package match

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/term"

	"github.com/ramtinJ95/tuido/internal/task"
)

// Candidate is one task plus the list it came from.
type Candidate struct {
	Task *task.Task
	Ref  string // workspace/list
}

// Locator is the `path:line` string used to address a candidate across a
// process boundary (the fzf preview command).
func (c Candidate) Locator() string {
	return fmt.Sprintf("%s:%d", c.Task.Path, c.Task.Line)
}

// ErrNoMatch is exit code 1: nothing matched.
type ErrNoMatch struct{ Query string }

func (e *ErrNoMatch) Error() string { return fmt.Sprintf("no task matches %q", e.Query) }

// ErrAmbiguous is exit code 1 in a non-interactive context. It carries the
// candidates so the caller can print them, which is what keeps every command
// scriptable.
type ErrAmbiguous struct {
	Query      string
	Candidates []Candidate
}

func (e *ErrAmbiguous) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%q matches %d tasks:", e.Query, len(e.Candidates))
	for _, c := range e.Candidates {
		fmt.Fprintf(&b, "\n  %s  %s", c.Ref, c.Task.Desc)
	}
	b.WriteString("\n  refine the query, or run interactively to pick one")
	return b.String()
}

// ErrCancelled means the user dismissed the picker.
type ErrCancelled struct{}

func (e *ErrCancelled) Error() string { return "cancelled" }

// Find returns every candidate whose description contains all of the query's
// tokens, best first.
//
// The ranking is deliberately simple and predictable rather than clever: sum of
// first-match offsets, then description length, then locator. A scoring library
// would make the same query rank differently as the corpus changes.
func Find(cands []Candidate, query string) []Candidate {
	tokens := strings.Fields(normalise(query))
	if len(tokens) == 0 {
		return cands
	}

	type scored struct {
		c      Candidate
		offset int
	}
	var out []scored
	for _, c := range cands {
		hay := normalise(c.Task.Desc)
		total, ok := 0, true
		for _, tok := range tokens {
			i := strings.Index(hay, tok)
			if i < 0 {
				ok = false
				break
			}
			total += i
		}
		if ok {
			out = append(out, scored{c, total})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].offset != out[j].offset {
			return out[i].offset < out[j].offset
		}
		if len(out[i].c.Task.Desc) != len(out[j].c.Task.Desc) {
			return len(out[i].c.Task.Desc) < len(out[j].c.Task.Desc)
		}
		return out[i].c.Locator() < out[j].c.Locator()
	})

	res := make([]Candidate, len(out))
	for i, s := range out {
		res[i] = s.c
	}
	return res
}

// Resolve narrows candidates to exactly one, offering a picker when it can.
func Resolve(cands []Candidate, query string) (Candidate, error) {
	hits := Find(cands, query)
	switch len(hits) {
	case 0:
		return Candidate{}, &ErrNoMatch{Query: query}
	case 1:
		return hits[0], nil
	}
	if !Interactive() {
		return Candidate{}, &ErrAmbiguous{Query: query, Candidates: hits}
	}
	return pick(hits, query)
}

// Interactive reports whether a picker can be shown: stdin must be a terminal
// and fzf must be on PATH.
func Interactive() bool {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false
	}
	_, err := exec.LookPath("fzf")
	return err == nil
}

// pick shells out to fzf. The first tab-delimited column is the locator, hidden
// from display but used by the preview command.
func pick(cands []Candidate, query string) (Candidate, error) {
	var in strings.Builder
	index := map[string]Candidate{}
	for _, c := range cands {
		loc := c.Locator()
		index[loc] = c
		fmt.Fprintf(&in, "%s\t%s\t%s\n", loc, c.Ref, c.Task.Desc)
	}

	self, err := os.Executable()
	if err != nil {
		self = "tuido"
	}
	args := []string{
		"--ansi",
		"--delimiter", "\t",
		"--with-nth", "2..",
		"--preview", quote(self) + " show {1}",
		"--preview-window", "right,60%",
		"--height", "40%",
		"--prompt", "task> ",
	}
	if query != "" {
		args = append(args, "--query", query)
	}

	cmd := exec.Command("fzf", args...)
	cmd.Stdin = strings.NewReader(in.String())
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 130 {
			return Candidate{}, &ErrCancelled{}
		}
		return Candidate{}, fmt.Errorf("fzf: %w", err)
	}
	loc, _, _ := strings.Cut(strings.TrimRight(string(out), "\n"), "\t")
	c, ok := index[loc]
	if !ok {
		return Candidate{}, &ErrCancelled{}
	}
	return c, nil
}

// PickString runs the picker over plain strings, for choosing a list rather
// than a task.
func PickString(items []string, prompt, query string) (string, error) {
	args := []string{"--height", "40%", "--prompt", prompt}
	if query != "" {
		args = append(args, "--query", query)
	}
	cmd := exec.Command("fzf", args...)
	cmd.Stdin = strings.NewReader(strings.Join(items, "\n") + "\n")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", &ErrCancelled{}
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// ParseLocator splits a `path:line` locator.
func ParseLocator(s string) (path string, line int, err error) {
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return "", 0, fmt.Errorf("bad locator %q", s)
	}
	line, err = strconv.Atoi(s[i+1:])
	if err != nil {
		return "", 0, fmt.Errorf("bad locator %q", s)
	}
	return s[:i], line, nil
}

// normalise lowercases, folds U+00A0 to a space and collapses runs of
// whitespace, so a query typed with ordinary spaces matches a line that uses
// non-breaking ones.
func normalise(s string) string {
	s = strings.ReplaceAll(s, "\u00a0", " ")
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

func quote(s string) string {
	if !strings.ContainsAny(s, " \t'\"") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
