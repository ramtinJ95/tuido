package main

import (
	"fmt"
	"regexp"
	"runtime/debug"
	"strings"
)

// version is set by the linker for release builds (see the Makefile). For a
// `go install` build no linker flags are applied, so it stays "dev" and the
// real version has to come from the embedded build info instead.
var version = "dev"

// tuidoVersion reports the running binary's version.
//
// Without this, anyone who installed with `go install …@latest` sees "dev"
// forever and has no way to tell whether they are behind — which would make the
// upgrade check meaningless.
func tuidoVersion() string {
	if version != "dev" && version != "" {
		return version
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	// `go install pkg@v0.2.0` stamps the module version here.
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return v
	}
	// A local `go build` has no module version, but does record the commit.
	var rev string
	var dirty bool
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if dirty {
		rev += "-dirty"
	}
	return "dev+" + rev
}

var (
	releaseVersion     = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?$`)
	gitDescribeVersion = regexp.MustCompile(`-[0-9]+-g[0-9a-f]+(?:-dirty)?$`)
)

func isReleaseVersion(v string) bool {
	return releaseVersion.MatchString(v) &&
		!gitDescribeVersion.MatchString(v) &&
		!strings.HasSuffix(v, "-dirty")
}

// released reports whether this is a real tagged build. Go marks local builds
// from a tagged checkout with build metadata such as v0.1.1+dirty; that must
// not opt a development binary into background update checks.
func released() bool {
	return isReleaseVersion(tuidoVersion())
}

func printVersion() {
	fmt.Printf("tuido %s\n", tuidoVersion())
}
