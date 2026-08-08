package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// The fake GitHub is served over an in-memory pipe rather than a TCP port, so
// these tests run in any environment, including one that forbids binding a
// socket.
type memListener struct {
	conns chan net.Conn
	done  chan struct{}
}

func newMemListener() *memListener {
	return &memListener{conns: make(chan net.Conn), done: make(chan struct{})}
}

func (l *memListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *memListener) Close() error {
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return nil
}

func (l *memListener) Addr() net.Addr { return memAddr{} }

func (l *memListener) dial() (net.Conn, error) {
	server, client := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

type memAddr struct{}

func (memAddr) Network() string { return "mem" }
func (memAddr) String() string  { return "memory" }

// serve starts an HTTP server on an in-memory listener and returns a client
// wired to it.
func serve(t *testing.T, h http.Handler) (base string, client *http.Client) {
	t.Helper()
	lis := newMemListener()
	srv := &http.Server{Handler: h}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { _ = srv.Close(); _ = lis.Close() })

	return "http://memory", &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true, // one pipe per request
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return lis.dial()
			},
		},
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"v0.1.0", "v0.2.0", true},
		{"v0.2.0", "v0.1.0", false},
		{"v0.1.0", "v0.1.0", false},
		{"v1.0.0", "v1.0.1", true},
		{"v0.1.0", "v1.0.0", true},
		// A string compare would get this one backwards.
		{"v0.9.0", "v0.10.0", true},
		{"v0.10.0", "v0.9.0", false},
		// A development build is never "outdated": it has nothing to compare.
		{"dev", "v9.9.9", false},
		{"dev+abc123", "v9.9.9", false},
		// A final release supersedes its own pre-release, but not vice versa.
		{"v0.2.0-rc1", "v0.2.0", true},
		{"v0.2.0", "v0.2.0-rc1", false},
		// Garbage is never newer.
		{"v0.1.0", "not-a-version", false},
		{"v0.1.0", "", false},
	}
	for _, c := range cases {
		if got := Newer(c.current, c.latest); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

// fakeGitHub serves a release with a correctly checksummed binary.
func fakeGitHub(t *testing.T, tag string, payload []byte, corruptSum bool) *Client {
	t.Helper()
	asset := AssetName(runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(payload)
	hexSum := hex.EncodeToString(sum[:])
	if corruptSum {
		hexSum = hex.EncodeToString(make([]byte, 32))
	}

	mux := http.NewServeMux()
	base, httpClient := serve(t, mux)

	mux.HandleFunc("/repos/test/tuido/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{
		  "tag_name": %q,
		  "html_url": "https://example.invalid/releases/%s",
		  "assets": [
		    {"name": %q, "browser_download_url": "%s/asset"},
		    {"name": %q, "browser_download_url": "%s/sums"}
		  ]
		}`, tag, tag, asset, base, ChecksumsName, base)
	})
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n%s  tuido_other_platform\n", hexSum, asset, hexSum)
	})
	return &Client{Repo: "test/tuido", APIBase: base, HTTP: httpClient}
}

func TestLatestAndDownload(t *testing.T) {
	payload := []byte("#!/bin/sh\necho new tuido\n")
	c := fakeGitHub(t, "v0.2.0", payload, false)

	rel, err := c.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if rel.Version != "v0.2.0" {
		t.Errorf("version = %q", rel.Version)
	}
	got, err := c.Download(rel)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Errorf("payload = %q", got)
	}
}

// Installing unverified bytes over the binary the user runs is the one place
// this package could do real harm, so a bad checksum must be fatal.
func TestChecksumMismatchIsRefused(t *testing.T) {
	c := fakeGitHub(t, "v0.2.0", []byte("payload"), true)
	rel, err := c.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Download(rel); err == nil {
		t.Fatal("a mismatched checksum was accepted")
	} else if !contains(err.Error(), "checksum mismatch") {
		t.Errorf("err = %v", err)
	}
}

func TestMissingChecksumsIsRefused(t *testing.T) {
	c := fakeGitHub(t, "v0.2.0", []byte("payload"), false)
	rel, err := c.Latest()
	if err != nil {
		t.Fatal(err)
	}
	delete(rel.Assets, ChecksumsName)
	if _, err := c.Download(rel); err == nil {
		t.Fatal("a release with no checksums was accepted")
	}
}

func TestMissingPlatformAssetIsNamed(t *testing.T) {
	c := fakeGitHub(t, "v0.2.0", []byte("payload"), false)
	rel, _ := c.Latest()
	delete(rel.Assets, AssetName(runtime.GOOS, runtime.GOARCH))
	_, err := c.Download(rel)
	if err == nil {
		t.Fatal("missing platform asset was accepted")
	}
	if !contains(err.Error(), runtime.GOOS) {
		t.Errorf("error does not name the platform: %v", err)
	}
}

func TestNoReleasesYet(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	base, httpClient := serve(t, mux)

	c := &Client{Repo: "test/tuido", APIBase: base, HTTP: httpClient}
	_, err := c.Latest()
	if err == nil || !contains(err.Error(), "no releases") {
		t.Errorf("err = %v", err)
	}
}

func TestReplaceIsAtomicAndKeepsMode(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "tuido")
	if err := os.WriteFile(bin, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Replace(bin, []byte("new")); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("contents = %q", got)
	}
	fi, _ := os.Stat(bin)
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("mode = %v, want 0755", fi.Mode().Perm())
	}
	// No debris left in the directory.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temp files left behind: %d entries", len(entries))
	}
}

func TestReplaceOnUnwritableDirIsActionable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permissions do not apply")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "tuido")
	if err := os.WriteFile(bin, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	err := Replace(bin, []byte("new"))
	if err == nil {
		t.Fatal("write to an unwritable directory succeeded")
	}
	if !contains(err.Error(), "reinstall") {
		t.Errorf("error is not actionable: %v", err)
	}
}

func TestStateRoundTripAndDue(t *testing.T) {
	dir := t.TempDir()
	if !Due(dir, time.Hour) {
		t.Error("a missing cache should be due for a check")
	}
	s := State{LastCheck: time.Now().Unix(), Latest: "v0.3.0", URL: "https://example.invalid"}
	if err := WriteState(dir, s); err != nil {
		t.Fatal(err)
	}
	got := ReadState(dir)
	if got.Latest != "v0.3.0" || got.URL != s.URL {
		t.Errorf("state = %+v", got)
	}
	if Due(dir, time.Hour) {
		t.Error("a fresh cache should not be due")
	}
	if !Due(dir, time.Nanosecond) {
		t.Error("an expired cache should be due")
	}
}

// A failed check is recorded, not propagated: it must never break a command.
func TestCheckRecordsFailure(t *testing.T) {
	dir := t.TempDir()
	lis := newMemListener()
	_ = lis.Close() // nothing is listening
	c := &Client{Repo: "test/tuido", APIBase: "http://memory", HTTP: &http.Client{
		Timeout: time.Second,
		Transport: &http.Transport{DialContext: func(ctx context.Context, n, a string) (net.Conn, error) {
			return lis.dial()
		}},
	}}
	if _, err := c.Check(dir); err == nil {
		t.Fatal("unreachable host did not error")
	}
	if s := ReadState(dir); s.LastError == "" {
		t.Error("failure not recorded in the cache")
	} else if s.Latest != "" {
		t.Error("a failed check invented a version")
	}
	// And the failure counts as a check, so it does not retry on every command.
	if Due(dir, time.Hour) {
		t.Error("a failed check should still reset the interval")
	}
}

func TestChecksumParsing(t *testing.T) {
	manifest := "abc123  tuido_darwin_arm64\ndef456 *tuido_linux_amd64\n"
	if got, err := checksumFor(manifest, "tuido_darwin_arm64"); err != nil || got != "abc123" {
		t.Errorf("got %q, %v", got, err)
	}
	// shasum's binary-mode marker must not defeat the lookup.
	if got, err := checksumFor(manifest, "tuido_linux_amd64"); err != nil || got != "def456" {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := checksumFor(manifest, "tuido_windows_amd64"); err == nil {
		t.Error("missing entry did not error")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
