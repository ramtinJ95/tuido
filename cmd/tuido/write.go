package main

import (
	"bufio"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/pflag"

	"github.com/ramtinJ95/tuido/internal/match"
	"github.com/ramtinJ95/tuido/internal/store"
	"github.com/ramtinJ95/tuido/internal/task"
)

// cmdAdd captures a task. Flags may appear before or after the text.
func cmdAdd(args []string) error {
	fs := pflag.NewFlagSet("add", pflag.ContinueOnError)
	root, ws := common(fs)
	var (
		prio = fs.StringP("prio", "p", "", "highest|high|medium|low|lowest")
		due  = fs.StringP("due", "d", "", "YYYY-MM-DD | today | tomorrow | week | <weekday>")
		tags = fs.StringSliceP("tag", "t", nil, "tag to append (repeatable)")
		list = fs.StringP("list", "l", "", "destination list, or list/section")
		all  = fs.Bool("all", false, "search every workspace for the destination list")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	a, err := openApp(*root, *ws)
	if err != nil {
		return err
	}

	texts, err := gatherText(fs.Args())
	if err != nil {
		return err
	}
	if len(texts) == 0 {
		return uerr("nothing to add")
	}

	p := task.Normal
	if *prio != "" {
		if p, err = task.ParsePriority(*prio); err != nil {
			return uerr("%v", err)
		}
	}
	var dueDate *task.Date
	if *due != "" {
		d, err := parseWhen(*due)
		if err != nil {
			return uerr("%v", err)
		}
		dueDate = &d
	}

	target, err := a.resolveTarget(*list, *all)
	if err != nil {
		return err
	}

	today := task.Today()
	for _, text := range texts {
		k := &task.Task{
			State:  task.Open,
			Desc:   strings.TrimSpace(text),
			Bullet: "-",
		}
		k.SetPriority(p)
		k.SetDue(dueDate)
		k.SetCreated(&today)
		for _, tag := range *tags {
			k.AddTag(tag)
		}
		if err := k.Rewritable(); err != nil {
			return uerr("%v", err)
		}
		if err := a.st.Add(target, k); err != nil {
			return err
		}
		fmt.Printf("✓ %s → %s\n", k.Desc, target.Ref())
		a.commit(target.List.Path, fmt.Sprintf("add: %s (%s)", k.Desc, target.Ref()))
	}
	return nil
}

// resolveTarget picks the destination, offering the picker when a -l value is
// ambiguous.
func (a *app) resolveTarget(list string, all bool) (store.Target, error) {
	t, err := a.st.ResolveTarget(list, all)
	var ambig *store.ErrAmbiguousList
	if err == nil || !errors.As(err, &ambig) || !match.Interactive() {
		return t, err
	}
	refs := make([]string, len(ambig.Candidates))
	byRef := map[string]store.List{}
	for i, l := range ambig.Candidates {
		refs[i] = l.Ref()
		byRef[l.Ref()] = l
	}
	chosen, perr := match.PickString(refs, "list> ", list)
	if perr != nil {
		return store.Target{}, perr
	}
	l, ok := byRef[chosen]
	if !ok {
		return store.Target{}, &match.ErrCancelled{}
	}
	return store.Target{List: l}, nil
}

// gatherText resolves the three input paths: arguments, stdin (`tuido add -`),
// and $EDITOR on a scratch buffer when there is nothing else.
func gatherText(args []string) ([]string, error) {
	if len(args) == 1 && args[0] == "-" {
		return readLines(os.Stdin)
	}
	if len(args) > 0 {
		return []string{strings.Join(args, " ")}, nil
	}
	return editorLines()
}

func readLines(r io.Reader) ([]string, error) {
	var out []string
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			out = append(out, line)
		}
	}
	return out, sc.Err()
}

func editorLines() ([]string, error) {
	f, err := os.CreateTemp("", "tuido-*.md")
	if err != nil {
		return nil, err
	}
	name := f.Name()
	defer os.Remove(name)
	_, _ = f.WriteString("\n# one task per line; blank lines and this comment are ignored\n")
	f.Close()

	if err := runEditor(name); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, nil
}

func editorCommand() []string {
	for _, env := range []string{"TUIDO_EDITOR", "VISUAL", "EDITOR"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return strings.Fields(v)
		}
	}
	return []string{"nvim"}
}

func runEditor(args ...string) error {
	ed := editorCommand()
	cmd := exec.Command(ed[0], append(ed[1:], args...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

// cmdDone marks a task done. It does not move the line: sinking happens on sort,
// so the diff stays one line.
func cmdDone(args []string) error {
	fs := pflag.NewFlagSet("done", pflag.ContinueOnError)
	root, ws := common(fs)
	all := fs.Bool("all", false, "search every workspace")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 && !match.Interactive() {
		return uerr("usage: tuido done <fuzzy text…>")
	}

	a, err := openApp(*root, *ws)
	if err != nil {
		return err
	}
	lists, err := a.st.Scope(*all)
	if err != nil {
		return err
	}
	cands, err := a.candidates(lists, true)
	if err != nil {
		return err
	}
	query := strings.Join(fs.Args(), " ")
	hit, err := match.Resolve(cands, query)
	if err != nil {
		return err
	}
	if err := hit.Task.Rewritable(); err != nil {
		return uerr("%v", err)
	}

	openIDs := a.st.OpenIDs(lists)
	if hit.Task.Blocked(openIDs) {
		// Allowed, but worth saying out loud.
		fmt.Printf("⚠ %s is blocked by %s\n", hit.Task.Desc, strings.Join(hit.Task.BlockedBy, ", "))
	}

	f, k, err := reread(a, hit)
	if err != nil {
		return err
	}
	today := task.Today()
	k.SetState(task.Done)
	k.SetCompleted(&today)
	if err := a.st.Write(f); err != nil {
		return err
	}
	fmt.Printf("✓ done: %s → %s\n", k.Desc, hit.Ref)
	a.commit(f.Path, fmt.Sprintf("done: %s (%s)", k.Desc, hit.Ref))
	return nil
}

// cmdID stamps a short random id on a task, so it can be named by ⛔. Never
// called automatically: this is the opt-in escape hatch.
func cmdID(args []string) error {
	fs := pflag.NewFlagSet("id", pflag.ContinueOnError)
	root, ws := common(fs)
	all := fs.Bool("all", false, "search every workspace")
	if err := fs.Parse(args); err != nil {
		return err
	}
	a, err := openApp(*root, *ws)
	if err != nil {
		return err
	}
	lists, err := a.st.Scope(*all)
	if err != nil {
		return err
	}
	cands, err := a.candidates(lists, false)
	if err != nil {
		return err
	}
	hit, err := match.Resolve(cands, strings.Join(fs.Args(), " "))
	if err != nil {
		return err
	}
	if hit.Task.ID != "" {
		fmt.Println(hit.Task.ID)
		return nil
	}
	if err := hit.Task.Rewritable(); err != nil {
		return uerr("%v", err)
	}

	f, k, err := reread(a, hit)
	if err != nil {
		return err
	}
	id, err := shortID()
	if err != nil {
		return err
	}
	k.SetID(id)
	if err := a.st.Write(f); err != nil {
		return err
	}
	fmt.Println(id)
	a.commit(f.Path, fmt.Sprintf("id: %s (%s)", k.Desc, hit.Ref))
	return nil
}

// reread re-parses the file the match came from and returns the same task from
// that fresh parse, so the write is based on the file as it is now.
func reread(a *app, hit match.Candidate) (*task.File, *task.Task, error) {
	b, err := os.ReadFile(hit.Task.Path)
	if err != nil {
		return nil, nil, err
	}
	f, err := task.Parse(hit.Task.Path, b)
	if err != nil {
		return nil, nil, err
	}
	idx := hit.Task.Line - 1
	if idx < 0 || idx >= len(f.Lines) || f.Lines[idx].Task == nil ||
		f.Lines[idx].Task.Raw() != hit.Task.Raw() {
		return nil, nil, uerr("%s changed on disk since it was matched — run the command again",
			filepath.Base(hit.Task.Path))
	}
	return f, f.Lines[idx].Task, nil
}

func shortID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:])), nil
}
