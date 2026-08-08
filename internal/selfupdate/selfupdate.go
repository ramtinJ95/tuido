// Package selfupdate checks GitHub for a newer release and installs it.
//
// The check is never made in front of a command: it runs in a detached
// background process and writes its answer to a cache file, exactly like the
// git sync, so the next foreground command can mention it in one line without
// having waited for anything. Installing is always explicit — tuido never
// replaces itself behind your back.
package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// DefaultRepo is the source of releases.
const DefaultRepo = "ramtinJ95/tuido"

// AssetName is what a release binary for a platform is called. `make release`
// produces exactly these names.
func AssetName(goos, goarch string) string {
	return fmt.Sprintf("tuido_%s_%s", goos, goarch)
}

// ChecksumsName is the sha256 manifest published alongside the binaries.
const ChecksumsName = "checksums.txt"

// Release is the subset of the GitHub release payload that matters.
type Release struct {
	Version string
	URL     string
	Assets  map[string]string // asset name -> download URL
}

// Client talks to the GitHub releases API.
type Client struct {
	Repo    string
	APIBase string // overridden in tests
	HTTP    *http.Client
}

// NewClient returns a client with a short timeout: this runs in a background
// process that must not linger.
func NewClient() *Client {
	return &Client{
		Repo:    DefaultRepo,
		APIBase: "https://api.github.com",
		HTTP:    &http.Client{Timeout: 20 * time.Second},
	}
}

// Latest fetches the newest published release.
func (c *Client) Latest() (Release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", strings.TrimSuffix(c.APIBase, "/"), c.Repo)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "tuido")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Release{}, fmt.Errorf("no releases published for %s yet", c.Repo)
	}
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("github returned %s", resp.Status)
	}

	var payload struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&payload); err != nil {
		return Release{}, err
	}
	rel := Release{Version: payload.TagName, URL: payload.HTMLURL, Assets: map[string]string{}}
	for _, a := range payload.Assets {
		rel.Assets[a.Name] = a.URL
	}
	if rel.Version == "" {
		return Release{}, fmt.Errorf("release has no tag")
	}
	return rel, nil
}

// Newer reports whether latest is a higher version than current.
//
// Comparison is numeric on the vX.Y.Z triple, so v0.10.0 correctly beats
// v0.9.0 — a string compare would get that backwards. A build that is not a
// tagged release is never considered outdated.
func Newer(current, latest string) bool {
	if !strings.HasPrefix(current, "v") {
		return false
	}
	cur, curPre, ok1 := parseVersion(current)
	lat, latPre, ok2 := parseVersion(latest)
	if !ok1 || !ok2 {
		return false
	}
	for i := 0; i < 3; i++ {
		if cur[i] != lat[i] {
			return lat[i] > cur[i]
		}
	}
	// Same triple: a final release supersedes a pre-release of itself.
	return curPre != "" && latPre == ""
}

func parseVersion(v string) (parts [3]int, pre string, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		pre, v = v[i+1:], v[:i]
	}
	fields := strings.Split(v, ".")
	if len(fields) != 3 {
		return parts, pre, false
	}
	for i, f := range fields {
		n, err := strconv.Atoi(f)
		if err != nil || n < 0 {
			return parts, pre, false
		}
		parts[i] = n
	}
	return parts, pre, true
}

// State is the cached answer, at <cache>/update.json.
type State struct {
	LastCheck int64  `json:"last_check"`
	Latest    string `json:"latest"`
	URL       string `json:"url"`
	LastError string `json:"last_error"`
}

// ReadState never fails: an update check must not be able to break a read
// command.
func ReadState(cacheDir string) State {
	var s State
	b, err := os.ReadFile(filepath.Join(cacheDir, "update.json"))
	if err != nil {
		return s
	}
	_ = json.Unmarshal(b, &s)
	return s
}

// WriteState records the outcome of a check.
func WriteState(cacheDir string, s State) error {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cacheDir, "update.json"), b, 0o644)
}

// Due reports whether the cached answer is older than interval.
func Due(cacheDir string, interval time.Duration) bool {
	return time.Since(time.Unix(ReadState(cacheDir).LastCheck, 0)) >= interval
}

// Check fetches the latest release and caches the result.
func (c *Client) Check(cacheDir string) (State, error) {
	s := State{LastCheck: time.Now().Unix()}
	rel, err := c.Latest()
	if err != nil {
		s.LastError = err.Error()
		_ = WriteState(cacheDir, s)
		return s, err
	}
	s.Latest, s.URL = rel.Version, rel.URL
	return s, WriteState(cacheDir, s)
}

// Download fetches the release binary for this platform, verifies it against
// the published checksum, and returns the bytes.
//
// The checksum is not optional. Replacing the binary the user runs with
// unverified bytes off the network is the one place this package could do real
// harm, so a missing or mismatched checksum is a hard failure.
func (c *Client) Download(rel Release) ([]byte, error) {
	name := AssetName(runtime.GOOS, runtime.GOARCH)
	assetURL, ok := rel.Assets[name]
	if !ok {
		return nil, fmt.Errorf("release %s has no binary for %s/%s (looked for %s)",
			rel.Version, runtime.GOOS, runtime.GOARCH, name)
	}
	sumsURL, ok := rel.Assets[ChecksumsName]
	if !ok {
		return nil, fmt.Errorf("release %s publishes no %s, refusing to install unverified bytes",
			rel.Version, ChecksumsName)
	}

	sums, err := c.get(sumsURL, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", ChecksumsName, err)
	}
	want, err := checksumFor(string(sums), name)
	if err != nil {
		return nil, err
	}

	body, err := c.get(assetURL, 200<<20)
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", name, err)
	}
	got := sha256.Sum256(body)
	if hex.EncodeToString(got[:]) != want {
		return nil, fmt.Errorf("checksum mismatch for %s: refusing to install", name)
	}
	return body, nil
}

func (c *Client) get(url string, limit int64) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "tuido")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("got %s", resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func checksumFor(manifest, name string) (string, error) {
	for _, line := range strings.Split(manifest, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		// `shasum -a 256` writes "<sum>  <name>", sometimes with a * marker.
		if strings.TrimPrefix(fields[1], "*") == name {
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("%s lists no checksum for %s", ChecksumsName, name)
}

// Replace atomically swaps the binary at path for the given bytes.
//
// The temp file is created in the destination directory so the rename stays
// atomic, and permissions are copied from the binary being replaced. On Unix a
// running binary can be renamed over safely: the running process keeps its
// original inode.
func Replace(path string, body []byte) error {
	dir := filepath.Dir(path)
	mode := os.FileMode(0o755)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}

	tmp, err := os.CreateTemp(dir, ".tuido-upgrade-*")
	if err != nil {
		return notWritable(dir, err)
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return notWritable(dir, err)
	}
	return nil
}

// notWritable turns a bare permission error into something actionable, rather
// than leaving the user staring at "operation not permitted".
func notWritable(dir string, err error) error {
	if os.IsPermission(err) {
		return fmt.Errorf("cannot write to %s: %w\n"+
			"  tuido is installed somewhere you do not own — either re-run with sudo,\n"+
			"  or reinstall into a directory you own (for example ~/.local/bin)", dir, err)
	}
	return err
}
