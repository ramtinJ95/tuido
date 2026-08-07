package task

import "strings"

// Block is a maximal run of consecutive task lines at the same base indent,
// together with each task's continuation lines and nested children.
//
// Anything the user wrote around it — headings, prose, blank lines, fences,
// the marker comment — is a boundary. tuido moves task lines within a block and
// nothing else, ever.
type Block struct {
	Start, End int    // inclusive indices into File.Lines
	Section    string // nearest preceding heading text, "" at top of file
	Indent     string // base indent of the top-level items
	Items      []Item // top-level items, in file order
}

// Item is one top-level task and everything that travels with it: its
// continuation lines and its nested subtasks.
type Item struct {
	Start, End int // inclusive indices into File.Lines
	Task       *Task
}

func findBlocks(lines []Line) []Block {
	var (
		blocks  []Block
		section string
	)
	for i := 0; i < len(lines); i++ {
		switch lines[i].Kind {
		case LineHeading:
			section = strings.TrimSpace(strings.TrimLeft(lines[i].Raw, "# "))
			continue
		case LineTask:
			// fall through to block collection
		default:
			continue
		}

		base := indentWidth(lines[i].Task.Indent)
		b := Block{Start: i, Section: section, Indent: lines[i].Task.Indent}

		end := i
		for j := i; j < len(lines); j++ {
			k := lines[j].Kind
			if k == LineTaskCont {
				end = j
				continue
			}
			if k != LineTask {
				break
			}
			if indentWidth(lines[j].Task.Indent) < base {
				break // a shallower task starts a new block
			}
			if indentWidth(lines[j].Task.Indent) == base {
				b.Items = append(b.Items, Item{Start: j, Task: lines[j].Task})
			}
			end = j
		}
		b.End = end

		for n := range b.Items {
			if n+1 < len(b.Items) {
				b.Items[n].End = b.Items[n+1].Start - 1
			} else {
				b.Items[n].End = b.End
			}
		}

		blocks = append(blocks, b)
		i = end
	}
	return blocks
}

// ReorderBlock rewrites the block's lines in the order given by perm, which is
// a permutation of block item indices. It returns the number of top-level items
// that changed position.
//
// Line terminators are reassigned by position, not carried with the line, so a
// file whose last line has no trailing newline keeps that property.
func (f *File) ReorderBlock(b Block, perm []int) int {
	if len(perm) != len(b.Items) {
		panic("ReorderBlock: perm length mismatch")
	}
	moved := 0
	for i, p := range perm {
		if i != p {
			moved++
		}
	}
	if moved == 0 {
		return 0
	}

	eols := make([]string, 0, b.End-b.Start+1)
	for i := b.Start; i <= b.End; i++ {
		eols = append(eols, f.Lines[i].EOL)
	}

	out := make([]Line, 0, b.End-b.Start+1)
	for _, p := range perm {
		it := b.Items[p]
		out = append(out, f.Lines[it.Start:it.End+1]...)
	}
	copy(f.Lines[b.Start:b.End+1], out)
	for i := range eols {
		f.Lines[b.Start+i].EOL = eols[i]
	}

	f.renumber()
	f.Blocks = findBlocks(f.Lines)
	return moved
}

// SectionRange returns the line range covered by the heading whose text matches
// name (case-insensitively), or ok=false if there is no such heading. The range
// runs from the heading line to the line before the next heading of the same or
// higher level.
func (f *File) SectionRange(name string) (start, end int, ok bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	for i, ln := range f.Lines {
		if ln.Kind != LineHeading {
			continue
		}
		text := strings.TrimSpace(strings.TrimLeft(ln.Raw, "# "))
		if strings.ToLower(text) != name {
			continue
		}
		level := headingLevel(ln.Raw)
		end = len(f.Lines) - 1
		for j := i + 1; j < len(f.Lines); j++ {
			if f.Lines[j].Kind == LineHeading && headingLevel(f.Lines[j].Raw) <= level {
				end = j - 1
				break
			}
		}
		return i, end, true
	}
	return 0, 0, false
}

// Sections lists every heading in the file, in order.
func (f *File) Sections() []string {
	var out []string
	for _, ln := range f.Lines {
		if ln.Kind == LineHeading {
			out = append(out, strings.TrimSpace(strings.TrimLeft(ln.Raw, "# ")))
		}
	}
	return out
}

func headingLevel(raw string) int {
	n := 0
	for _, r := range raw {
		if r != '#' {
			break
		}
		n++
	}
	return n
}
