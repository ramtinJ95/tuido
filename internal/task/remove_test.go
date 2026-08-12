package task

import "testing"

func TestRemoveLines(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		start, end int
		want       string
	}{
		{
			name:  "middle range",
			in:    "# H\n- [x] a\n- [ ] b\n",
			start: 1, end: 1,
			want: "# H\n- [ ] b\n",
		},
		{
			name:  "item with continuation and subtree",
			in:    "- [x] parent\n  note line\n  - [x] child\n- [ ] next\n",
			start: 0, end: 2,
			want: "- [ ] next\n",
		},
		{
			name:  "tail range keeps missing trailing newline",
			in:    "- [ ] keep\n- [x] gone",
			start: 1, end: 1,
			want: "- [ ] keep",
		},
		{
			name:  "tail range after terminated line",
			in:    "- [ ] keep\n- [x] gone\n",
			start: 1, end: 1,
			want: "- [ ] keep\n",
		},
		{
			name:  "crlf preserved on remaining lines",
			in:    "- [x] gone\r\n- [ ] keep\r\n",
			start: 0, end: 0,
			want: "- [ ] keep\r\n",
		},
		{
			name:  "whole file",
			in:    "- [x] only\n",
			start: 0, end: 0,
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := Parse("t.md", []byte(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			f.RemoveLines(tc.start, tc.end)
			if got := string(f.Bytes()); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRemoveLinesReindexes checks that line numbers and blocks are rebuilt, so
// a later mutation through the same File still lands on the right line.
func TestRemoveLinesReindexes(t *testing.T) {
	f, err := Parse("t.md", []byte("- [x] gone\n- [ ] a\n- [ ] b\n"))
	if err != nil {
		t.Fatal(err)
	}
	f.RemoveLines(0, 0)
	tasks := f.Tasks()
	if len(tasks) != 2 || tasks[0].Line != 1 || tasks[1].Line != 2 {
		t.Fatalf("tasks not renumbered: %+v", tasks)
	}
	if len(f.Blocks) != 1 || len(f.Blocks[0].Items) != 2 {
		t.Fatalf("blocks not rebuilt: %+v", f.Blocks)
	}
}

func TestHeadingHelpers(t *testing.T) {
	if got := HeadingLevel("### Deep"); got != 3 {
		t.Errorf("HeadingLevel = %d, want 3", got)
	}
	if got := HeadingText("##  Spaced  "); got != "Spaced" {
		t.Errorf("HeadingText = %q, want %q", got, "Spaced")
	}
}
