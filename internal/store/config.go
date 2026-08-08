// Package store resolves workspaces and lists on disk, reads and writes task
// files atomically, and owns the machine-local config and context.
//
// Nothing tuido needs to run lives inside the task repo: the repo holds only
// tasks, so it stays portable and contains nothing machine-specific. The cost,
// accepted deliberately, is that config does not sync between machines.
package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

// ErrNotInitialised means there is no config and no root was supplied. Commands
// turn this into exit code 4 (or an interactive `tuido init`), never a guess.
var ErrNotInitialised = errors.New("tuido is not initialised — run `tuido init`")

// Config is ~/.config/tuido/config.toml.
type Config struct {
	Root             string `toml:"root"`
	DefaultWorkspace string `toml:"default_workspace"`
	CaptureList      string `toml:"capture_list"`
	Git              Git    `toml:"git"`
	Update           Update `toml:"update"`
}

// Update controls the background check for a newer tuido. The check never
// happens in front of a command; it is a detached process whose answer is read
// from cache, so turning it off costs nothing either way.
type Update struct {
	Check    bool   `toml:"check"`
	Interval string `toml:"interval"`
}

// CheckInterval is how stale the cached answer may be before a background
// check is kicked off. A malformed value falls back to the default rather than
// failing a read command.
func (u Update) CheckInterval() time.Duration {
	d, err := time.ParseDuration(u.Interval)
	if err != nil || d <= 0 {
		return 24 * time.Hour
	}
	return d
}

// Git holds the sync settings. All of it is per-machine on purpose: a laptop
// that should not push can say so without touching the shared repo.
type Git struct {
	Enabled  bool   `toml:"enabled"`
	AutoPush bool   `toml:"auto_push"`
	Fetch    string `toml:"fetch_interval"`
}

// DefaultConfig is what `tuido init` starts from.
func DefaultConfig() Config {
	return Config{
		DefaultWorkspace: "work",
		CaptureList:      "inbox",
		Git:              Git{Enabled: true, AutoPush: true, Fetch: "60s"},
		Update:           Update{Check: true, Interval: "24h"},
	}
}

// FetchInterval is how stale the last fetch may be before a command kicks off a
// background one. A malformed value falls back to the default rather than
// failing a read command.
func (g Git) FetchInterval() time.Duration {
	d, err := time.ParseDuration(g.Fetch)
	if err != nil || d <= 0 {
		return 60 * time.Second
	}
	return d
}

func xdgDir(env, fallback, app string) string {
	if v := os.Getenv(env); v != "" {
		return filepath.Join(v, app)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, filepath.FromSlash(fallback), app)
}

// ConfigDir is ~/.config/tuido.
func ConfigDir() string { return xdgDir("XDG_CONFIG_HOME", ".config", "tuido") }

// StateDir is ~/.local/state/tuido.
func StateDir() string { return xdgDir("XDG_STATE_HOME", ".local/state", "tuido") }

// CacheDir is ~/.cache/tuido.
func CacheDir() string { return xdgDir("XDG_CACHE_HOME", ".cache", "tuido") }

// ConfigPath is the config file.
func ConfigPath() string { return filepath.Join(ConfigDir(), "config.toml") }

// ContextPath holds the current workspace, one line.
func ContextPath() string { return filepath.Join(StateDir(), "context") }

// LoadConfig reads the config file. A missing file is ErrNotInitialised; a
// malformed one is an error in its own right, never a silent default.
func LoadConfig() (Config, error) {
	b, err := os.ReadFile(ConfigPath())
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, ErrNotInitialised
	}
	if err != nil {
		return Config{}, err
	}
	cfg := DefaultConfig()
	if err := toml.Unmarshal(b, &cfg); err != nil {
		return Config{}, fmt.Errorf("%s: %w", ConfigPath(), err)
	}
	return cfg, nil
}

// SaveConfig writes the config file, creating the directory if needed.
func SaveConfig(cfg Config) error {
	if err := os.MkdirAll(ConfigDir(), 0o755); err != nil {
		return err
	}
	b, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}
	header := "# tuido config — machine-local, deliberately not synced.\n" +
		"# Anything that should travel with your tasks belongs in a per-file\n" +
		"# `<!-- tuido: ... -->` marker instead.\n\n"
	return writeAtomic(ConfigPath(), append([]byte(header), b...), 0o644)
}

// ReadContext returns the persisted workspace, or "".
func ReadContext() string {
	b, err := os.ReadFile(ContextPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// WriteContext persists the current workspace.
func WriteContext(ws string) error {
	if err := os.MkdirAll(StateDir(), 0o755); err != nil {
		return err
	}
	return writeAtomic(ContextPath(), []byte(ws+"\n"), 0o644)
}

// ExpandPath resolves ~ and makes the path absolute, so a config value is never
// ambiguous once written.
func ExpandPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", errors.New("empty path")
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
	}
	return filepath.Abs(p)
}
