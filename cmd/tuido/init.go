package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/pflag"
	"golang.org/x/term"

	"github.com/ramtinJ95/tuido/internal/store"
)

// cmdInit is first-run setup. Every prompt has a flag equivalent, so a dotfiles
// bootstrap can run it non-interactively.
func cmdInit(args []string) error {
	fs := pflag.NewFlagSet("init", pflag.ContinueOnError)
	fs.SetInterspersed(true)
	var (
		root      = fs.String("root", "", "todo root directory")
		workspace = fs.String("workspace", "", "first workspace name (default \"work\")")
		useGit    = fs.Bool("git", true, "initialise a git repo in the root")
		noGit     = fs.Bool("no-git", false, "do not initialise a git repo")
		remote    = fs.String("remote", "", "git remote; if it has commits, clone it instead of initialising")
		force     = fs.Bool("force", false, "overwrite an existing config")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	interactive := term.IsTerminal(int(os.Stdin.Fd()))
	in := bufio.NewReader(os.Stdin)

	if _, err := store.LoadConfig(); err == nil && !*force {
		// Idempotent: re-running against a valid config changes nothing.
		fmt.Printf("already initialised — %s\n", store.ConfigPath())
		fmt.Println("  re-run with --force to overwrite")
		return nil
	} else if err != nil && !errors.Is(err, store.ErrNotInitialised) && !*force {
		return err
	}

	cfg := store.DefaultConfig()

	rootIn := *root
	if rootIn == "" {
		if !interactive {
			return uerr("--root is required when stdin is not a terminal")
		}
		rootIn = ask(in, "todo root", "~/todo")
	}
	rootAbs, err := store.ExpandPath(rootIn)
	if err != nil {
		return uerr("bad root path: %v", err)
	}
	cfg.Root = rootAbs

	wsName := *workspace
	if wsName == "" {
		wsName = "work"
	}

	cloned := false
	if *remote != "" && remoteHasCommits(*remote) {
		// Adopting an existing task repo is the common second-machine case: it
		// must clone, not initialise an empty repo next to it and diverge.
		if err := ensureEmptyOrAbsent(rootAbs); err != nil {
			return err
		}
		fmt.Printf("  cloning %s → %s\n", *remote, rootAbs)
		if out, err := run3("git", "clone", "--quiet", *remote, rootAbs); err != nil {
			return uerr("git clone: %s", out)
		}
		cloned = true
	}

	if !cloned {
		if err := createRoot(rootAbs, interactive, in, *root != ""); err != nil {
			return err
		}
	}

	gitWanted := *useGit && !*noGit
	if !cloned && interactive && !fs.Changed("git") && !fs.Changed("no-git") {
		gitWanted = askYesNo(in, "  initialize a git repo there?", true)
	}
	if !cloned && gitWanted {
		if _, err := os.Stat(filepath.Join(rootAbs, ".git")); os.IsNotExist(err) {
			if out, err := run3("git", "-C", rootAbs, "init", "--quiet"); err != nil {
				return uerr("git init: %s", out)
			}
		}
		remoteURL := *remote
		if remoteURL == "" && interactive && !fs.Changed("remote") {
			remoteURL = strings.TrimSpace(ask(in, "  git remote (blank to skip)", ""))
		}
		if remoteURL != "" {
			if out, err := run3("git", "-C", rootAbs, "remote", "add", "origin", remoteURL); err != nil {
				if !strings.Contains(out, "already exists") {
					return uerr("git remote add: %s", out)
				}
			}
		}
	}
	cfg.Git.Enabled = gitWanted || cloned

	if !cloned {
		if *workspace == "" && interactive {
			wsName = ask(in, "first workspace", "work")
		}
		wsDir := filepath.Join(rootAbs, wsName)
		if err := os.MkdirAll(wsDir, 0o755); err != nil {
			return err
		}
		inbox := filepath.Join(wsDir, cfg.CaptureList+".md")
		if _, err := os.Stat(inbox); os.IsNotExist(err) {
			// Deliberately heading-less: a file with headings is "structured",
			// and capture into a structured file refuses rather than burying
			// the task under an arbitrary section. The capture list must be the
			// one place that always accepts a bare `tuido add`.
			if err := os.WriteFile(inbox, nil, 0o644); err != nil {
				return err
			}
			fmt.Printf("  created %s\n", inbox)
		}
	} else {
		// The workspaces arrived with the clone; adopt the first one.
		if entries, err := os.ReadDir(rootAbs); err == nil {
			for _, e := range entries {
				if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
					wsName = e.Name()
					break
				}
			}
		}
	}
	cfg.DefaultWorkspace = wsName

	if err := store.SaveConfig(cfg); err != nil {
		return err
	}
	if err := store.WriteContext(wsName); err != nil {
		return err
	}
	fmt.Printf("✓ wrote %s\n", store.ConfigPath())
	fmt.Printf("✓ context set to `%s`\n", wsName)
	return nil
}

// createRoot makes the root directory, asking first when it does not exist and
// the user did not name it on the command line.
func createRoot(rootAbs string, interactive bool, in *bufio.Reader, fromFlag bool) error {
	fi, err := os.Stat(rootAbs)
	switch {
	case err == nil && fi.IsDir():
		return nil
	case err == nil:
		return uerr("%s exists and is not a directory", rootAbs)
	}
	if interactive && !fromFlag {
		if !askYesNo(in, fmt.Sprintf("  %s doesn't exist. create it?", rootAbs), true) {
			return uerr("aborted")
		}
	}
	return os.MkdirAll(rootAbs, 0o755)
}

// ensureEmptyOrAbsent refuses to clone over existing content.
func ensureEmptyOrAbsent(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return uerr("%s is not empty — clone into a fresh directory, or drop --remote", dir)
	}
	return nil
}

// remoteHasCommits distinguishes "adopt this repo" from "publish to this empty
// repo". It is the one place init touches the network.
func remoteHasCommits(url string) bool {
	out, err := run3("git", "ls-remote", "--heads", url)
	return err == nil && strings.TrimSpace(out) != ""
}

func run3(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func ask(in *bufio.Reader, prompt, def string) string {
	if def != "" {
		fmt.Printf("%s  [%s]: ", prompt, def)
	} else {
		fmt.Printf("%s: ", prompt)
	}
	line, _ := in.ReadString('\n')
	line = strings.TrimSpace(line)
	if line == "" {
		return def
	}
	return line
}

func askYesNo(in *bufio.Reader, prompt string, def bool) bool {
	hint := "[Y/n]"
	if !def {
		hint = "[y/N]"
	}
	fmt.Printf("%s %s ", prompt, hint)
	line, _ := in.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	}
	return def
}
