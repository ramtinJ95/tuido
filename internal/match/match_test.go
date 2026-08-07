package match

import (
	"strings"
	"testing"

	"github.com/ramtinJ95/tuido/internal/task"
)

func cands(t *testing.T, lines ...string) []Candidate {
	t.Helper()
	f, err := task.Parse("work/inbox.md", []byte(strings.Join(lines, "\n")+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	var out []Candidate
	for _, k := range f.Tasks() {
		out = append(out, Candidate{Task: k, Ref: "work/inbox"})
	}
	return out
}

func descs(cs []Candidate) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Task.Desc
	}
	return out
}

func TestEveryTokenMustMatch(t *testing.T) {
	cs := cands(t,
		"- [ ] rotate vault certs",
		"- [ ] rotate database certs",
		"- [ ] audit IAM policies",
	)
	got := descs(Find(cs, "rotate certs"))
	if len(got) != 2 {
		t.Fatalf("hits = %v, want both rotate tasks", got)
	}
	if got := descs(Find(cs, "rotate iam")); len(got) != 0 {
		t.Errorf("hits = %v, want none (tokens are ANDed)", got)
	}
	if got := descs(Find(cs, "VAULT")); len(got) != 1 {
		t.Errorf("matching is not case-insensitive: %v", got)
	}
}

// Ranking must be deterministic and explainable: earliest match first, then
// shortest description, then locator.
func TestRankingIsDeterministic(t *testing.T) {
	cs := cands(t,
		"- [ ] a long preamble before the word certs appears",
		"- [ ] certs",
		"- [ ] certs rotation runbook",
	)
	got := descs(Find(cs, "certs"))
	want := []string{"certs", "certs rotation runbook", "a long preamble before the word certs appears"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ranking = %v, want %v", got, want)
		}
	}
	// Same input, same order, every time.
	for i := 0; i < 5; i++ {
		if d := descs(Find(cs, "certs")); d[0] != want[0] || d[2] != want[2] {
			t.Fatalf("ranking is unstable: %v", d)
		}
	}
}

// Non-breaking spaces in the file must not defeat a query typed with ordinary
// ones.
func TestNBSPIsFolded(t *testing.T) {
	cs := cands(t, "- [ ] rotate\u00a0vault certs")
	if got := descs(Find(cs, "rotate vault")); len(got) != 1 {
		t.Errorf("hits = %v, want 1", got)
	}
}

func TestResolveBranches(t *testing.T) {
	cs := cands(t, "- [ ] alpha task", "- [ ] beta task")

	if _, err := Resolve(cs, "gamma"); err == nil {
		t.Error("no match did not error")
	} else if _, ok := err.(*ErrNoMatch); !ok {
		t.Errorf("err = %T, want *ErrNoMatch", err)
	}

	hit, err := Resolve(cs, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if hit.Task.Desc != "alpha task" {
		t.Errorf("resolved to %q", hit.Task.Desc)
	}

	// Tests never have a terminal, so this exercises the scriptable fallback:
	// print the candidates, exit 1, change nothing.
	_, err = Resolve(cs, "task")
	amb, ok := err.(*ErrAmbiguous)
	if !ok {
		t.Fatalf("err = %T, want *ErrAmbiguous", err)
	}
	if len(amb.Candidates) != 2 {
		t.Errorf("candidates = %d, want 2", len(amb.Candidates))
	}
	for _, want := range []string{"alpha task", "beta task", "refine the query"} {
		if !strings.Contains(amb.Error(), want) {
			t.Errorf("message missing %q:\n%s", want, amb.Error())
		}
	}
}

func TestEmptyQueryReturnsEverything(t *testing.T) {
	cs := cands(t, "- [ ] one", "- [ ] two")
	if got := Find(cs, ""); len(got) != 2 {
		t.Errorf("empty query returned %d candidates, want 2", len(got))
	}
}

func TestLocatorRoundTrip(t *testing.T) {
	cs := cands(t, "- [ ] first", "- [ ] second")
	loc := cs[1].Locator()
	path, line, err := ParseLocator(loc)
	if err != nil {
		t.Fatal(err)
	}
	if path != "work/inbox.md" || line != 2 {
		t.Errorf("ParseLocator(%q) = %q, %d", loc, path, line)
	}
	if _, _, err := ParseLocator("no-colon"); err == nil {
		t.Error("malformed locator did not error")
	}
	if _, _, err := ParseLocator("file.md:notanumber"); err == nil {
		t.Error("non-numeric line did not error")
	}
}
