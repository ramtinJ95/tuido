package main

import (
	"strings"
	"testing"

	"github.com/ramtinJ95/tuido/internal/selfupdate"
	"github.com/ramtinJ95/tuido/internal/store"
)

func TestUpdateNoticeReportsV040ToV032Users(t *testing.T) {
	oldVersion := version
	version = "v0.3.2"
	t.Cleanup(func() { version = oldVersion })
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg := store.DefaultConfig()
	if err := selfupdate.WriteState(store.CacheDir(), selfupdate.State{
		Latest: "v0.4.0",
		URL:    "https://github.com/ramtinJ95/tuido/releases/tag/v0.4.0",
	}); err != nil {
		t.Fatal(err)
	}

	got := updateNotice(cfg)
	for _, want := range []string{"v0.4.0 is available", "you have v0.3.2", "tuido upgrade"} {
		if !strings.Contains(got, want) {
			t.Errorf("notice %q does not contain %q", got, want)
		}
	}
}
