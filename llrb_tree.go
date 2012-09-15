package main

import (
	"bytes"
)

// LLRB tree with single key values as byte slices.
// I use 2-3 tree algorithms for it. Only insertion is implemented (no delete).
type llrb_tree struct {
	root *llrb_node
	count int
}

func (t *llrb_tree) insert_maybe(value []byte) {
	var ok bool
	t.root, ok = t.root.insert_maybe(value)
	if ok {
		t.count++
	}
}

func (t *llrb_tree) contains(value []byte) bool {
	return t.root.contains(value)
}

const (
	llrb_red = false
	llrb_black = true
)

type llrb_node struct {
	value []byte
	left *llrb_node
	right *llrb_node
	color bool
}

func (n *llrb_node) rotate_left() *llrb_node {
	x := n.right
	n.right = x.left
	x.left = n
	x.color = n.color
	n.color = llrb_red
	return x
}

func (n *llrb_node) rotate_right() *llrb_node {
	x := n.left
	n.left = x.right
	x.right = n
	x.color = n.color
	n.color = llrb_red
	return x
}

func (n *llrb_node) flip_colors() {
	n.color = !n.color
	n.left.color = !n.left.color
	n.right.color = !n.right.color
}

func (n *llrb_node) is_red() bool {
	return n != nil && !n.color
}

func (n *llrb_node) insert_maybe(value []byte) (*llrb_node, bool) {
	if n == nil {
		return &llrb_node{value: value}, true
	}

	var inserted bool
	switch cmp := bytes.Compare(value, n.value); {
	case cmp < 0:
		n.left, inserted = n.left.insert_maybe(value)
	case cmp > 0:
		n.right, inserted = n.right.insert_maybe(value)
	default:
		// don't insert anything
	}

	if n.right.is_red() && !n.left.is_red() {
		n = n.rotate_left()
	}
	if n.left.is_red() && n.left.left.is_red() {
		n = n.rotate_right()
	}
	if n.left.is_red() && n.right.is_red() {
		n.flip_colors()
	}

	return n, inserted
}

func (n *llrb_node) contains(value []byte) bool {
	for n != nil {
		switch cmp := bytes.Compare(value, n.value); {
		case cmp < 0:
			n = n.left
		case cmp > 0:
			n = n.right
		default:
			return true
		}
	}
	return false
}
