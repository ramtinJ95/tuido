package task

import (
	"path/filepath"
	"sort"
)

func globSorted(pat string) ([]string, error) {
	paths, err := filepath.Glob(pat)
	sort.Strings(paths)
	return paths, err
}
