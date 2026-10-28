package model

import (
	"fmt"
	"testing"
)

func TestTree(t *testing.T) {
	cases := []struct {
		ids, parents []string
		want         string
	}{
		{[]string{"a", "b", "c"}, []string{"", "", ""}, "[{0 0} {1 0} {2 0}]"},
		// c and d under a, e under c; b's parent is gone.
		{[]string{"a", "b", "c", "d", "e"}, []string{"", "x", "a", "a", "c"}, "[{0 0} {2 1} {4 2} {3 1} {1 0}]"},
		// A child listed before its parent still comes after it.
		{[]string{"c", "a"}, []string{"a", ""}, "[{1 0} {0 1}]"},
		// A cycle, and an item its own parent.
		{[]string{"a", "b", "c"}, []string{"b", "a", "c"}, "[{2 0} {0 0} {1 1}]"},
	}
	for _, c := range cases {
		got := fmt.Sprint(Tree(len(c.ids), func(i int) string { return c.ids[i] }, func(i int) string { return c.parents[i] }))
		if got != c.want {
			t.Errorf("%v %v: %s, want %s", c.ids, c.parents, got, c.want)
		}
	}
}
