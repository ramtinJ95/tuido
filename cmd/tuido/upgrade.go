package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ramtinJ95/tuido/internal/selfupdate"
	"github.com/ramtinJ95/tuido/internal/store"
)

// cmdUpgrade installs the latest release over the running binary.
//
// Always explicit. The background check only ever tells you a version exists;
// nothing downloads or replaces anything until you ask.
func cmdUpgrade(args []string) error {
	fs := newFlagSet("upgrade")
	fl := registerUpgrade(fs)
	if err := fs.Parse(args); err != nil {
		return flagErr(err)
	}

	self, err := os.Executable()
	if err != nil {
		return err
	}
	if self, err = resolveSymlinks(self); err != nil {
		return err
	}

	client := selfupdate.NewClient()
	current := tuidoVersion()

	fmt.Printf("current  %s\n", current)
	rel, err := client.Latest()
	if err != nil {
		return uerr("%v", err)
	}
	fmt.Printf("latest   %s\n", rel.Version)

	switch {
	case *fl.check:
		// Refresh the cache too, so the reminder line agrees with what was just
		// printed.
		_ = selfupdate.WriteState(store.CacheDir(), selfupdate.State{
			LastCheck: nowUnix(), Latest: rel.Version, URL: rel.URL,
		})
		if selfupdate.Newer(current, rel.Version) {
			fmt.Printf("\nA newer version is available. Run `tuido upgrade` to install it.\n%s\n", rel.URL)
		} else {
			fmt.Println("\nUp to date.")
		}
		return nil

	case !selfupdate.Newer(current, rel.Version) && !*fl.force:
		if !released() {
			fmt.Printf("\nThis is a development build, so there is nothing to compare against.\n" +
				"Use --force to install the latest release over it.\n")
			return nil
		}
		fmt.Println("\nAlready up to date.")
		return nil
	}

	fmt.Printf("\ninstalling %s → %s\n", rel.Version, self)
	body, err := client.Download(rel)
	if err != nil {
		return uerr("%v", err)
	}
	if err := selfupdate.Replace(self, body); err != nil {
		return uerr("%v", err)
	}
	_ = selfupdate.WriteState(store.CacheDir(), selfupdate.State{
		LastCheck: nowUnix(), Latest: rel.Version, URL: rel.URL,
	})
	fmt.Printf("✓ tuido %s\n", rel.Version)
	return nil
}

func nowUnix() int64 { return time.Now().Unix() }

// cmdInternalUpdateCheck is the detached background job. Undocumented: nothing
// but tuido should invoke it.
func cmdInternalUpdateCheck(args []string) error {
	if _, err := selfupdate.NewClient().Check(store.CacheDir()); err != nil {
		return nil // the failure is recorded in the cache; never noisy
	}
	return nil
}

// maybeCheckUpdate spawns the detached check if the cached answer is stale.
// It returns immediately — no command's latency depends on GitHub being up.
func maybeCheckUpdate(cfg store.Config) {
	if !cfg.Update.Check || !released() {
		return
	}
	if !selfupdate.Due(store.CacheDir(), cfg.Update.CheckInterval()) {
		return
	}
	self, err := os.Executable()
	if err != nil {
		return
	}
	if err := os.MkdirAll(store.CacheDir(), 0o755); err != nil {
		return
	}
	cmd := exec.Command(self, "internal-update-check")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // survive our exit
	cmd.Stdout, cmd.Stderr = nil, nil
	_ = cmd.Start() // never Wait
}

// updateNotice is the one line a foreground command prints when a newer
// release is already known about, or "".
func updateNotice(cfg store.Config) string {
	if !cfg.Update.Check || !released() {
		return ""
	}
	s := selfupdate.ReadState(store.CacheDir())
	if s.Latest == "" || !selfupdate.Newer(tuidoVersion(), s.Latest) {
		return ""
	}
	return fmt.Sprintf("tuido %s is available (you have %s) — run `tuido upgrade`",
		s.Latest, tuidoVersion())
}

// resolveSymlinks follows a symlinked binary to its target, so upgrading
// replaces the real file rather than turning the link into a regular file.
func resolveSymlinks(p string) (string, error) {
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p, nil // not fatal: fall back to the original path
	}
	return resolved, nil
}
