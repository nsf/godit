package main

import "testing"
import "strconv"
import "math/rand"
import "time"

// Test never fails, just use it with `go test -v`, it prints some values, looks
// like everything is fine.
func TestLLRBTree(t *testing.T) {
	var tree llrb_tree
	rand.Seed(time.Now().UnixNano())
	p := rand.Perm(1024)
	// insert 1024 different numbers
	for _, v := range p {
		var x []byte
		x = strconv.AppendInt(x, int64(v), 10)
		tree.insert_maybe(x)
	}
	tree.clear()
	// try inserting twice
	for _, v := range p {
		var x []byte
		x = strconv.AppendInt(x, int64(v), 10)
		tree.insert_maybe(x)
	}
	for _, v := range p {
		var x []byte
		x = strconv.AppendInt(x, int64(v), 10)
		tree.insert_maybe(x)
	}

	t.Logf("Length: %d\n", tree.count)
	// should be near 1/2
	t.Logf("Root: %s\n", string(tree.root.value))
	// should be near 1/4
	t.Logf("Left: %s\n", string(tree.root.left.value))
	// should be near 3/4
	t.Logf("Right: %s\n", string(tree.root.right.value))
	contains := func(n int) {
		var x []byte
		x = strconv.AppendInt(x, int64(n), 10)
		t.Logf("Contains: %d, %v\n", n, tree.contains(x))
	}
	contains(10)
	contains(0)
	contains(999)
	contains(54400)

	max_h := 0
	var traverse func(n *llrb_node, h int)
	traverse = func(n *llrb_node, h int) {
		if h > max_h {
			max_h = h
		}
		if n.left != nil {
			traverse(n.left, h+1)
		}
		if n.right != nil {
			traverse(n.right, h+1)
		}
	}
	traverse(tree.root, 0)

	// from what I've tested, max height seems to be 12 or 13, which is nice
	t.Logf("Max height: %d\n", max_h)

	// check if it's sorted correctly
	/*
		var printnodes func(n *llrb_node)
		printnodes = func(n *llrb_node) {
			if n == nil {
				return
			}
			printnodes(n.left)
			t.Logf("Node: %s\n", string(n.value))
			printnodes(n.right)
		}
		printnodes(tree.root)
	*/
	// seems correct, the order is lexicographic
}
