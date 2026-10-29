package model

// TreeNode is an item of a list ordered as a tree: its index in the list,
// and how deep it is (0 for a root).
type TreeNode struct{ Index, Depth int }

// Tree orders n items as a tree, as sessions under the session that
// started them: each after its parent, in the list's order otherwise. An
// item whose parent isn't among them is a root, and so is one in a cycle.
func Tree(n int, id, parent func(i int) string) []TreeNode {
	ids := make(map[string]bool, n)
	for i := range n {
		ids[id(i)] = true
	}
	children := map[string][]int{}
	var roots []int
	for i := range n {
		if p := parent(i); p != "" && p != id(i) && ids[p] {
			children[p] = append(children[p], i)
		} else {
			roots = append(roots, i)
		}
	}
	out := make([]TreeNode, 0, n)
	seen := make([]bool, n)
	var walk func(i, depth int)
	walk = func(i, depth int) {
		if seen[i] {
			return
		}
		seen[i] = true
		out = append(out, TreeNode{i, depth})
		for _, c := range children[id(i)] {
			walk(c, depth+1)
		}
	}
	for _, r := range roots {
		walk(r, 0)
	}
	for i := range n {
		walk(i, 0) // a cycle, with no root
	}
	return out
}

// TreeInsert is where a new item goes in a list ordered as a tree, for it
// to come last under parent: after the parent and what is under it; n
// when the parent isn't among the n items.
func TreeInsert(n int, id, parent func(i int) string, parentID string) int {
	index := make(map[string]int, n)
	for i := range n {
		index[id(i)] = i
	}
	at, ok := index[parentID]
	if !ok {
		return n
	}
	under := func(i int) bool {
		for hops := 0; hops < n; hops++ {
			p := parent(i)
			if p == parentID {
				return true
			}
			j, ok := index[p]
			if !ok {
				return false
			}
			i = j
		}
		return false
	}
	at++
	for at < n && under(at) {
		at++
	}
	return at
}
