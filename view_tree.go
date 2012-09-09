package main

import (
	"github.com/nsf/tulib"
)

//----------------------------------------------------------------------------
// view_tree
//----------------------------------------------------------------------------

type view_tree struct {
	// At the same time only one of these groups can be valid:
	// 1) 'left', 'right' and 'split'
	// 2) 'top', 'bottom' and 'split'
	// 3) 'leaf'
	parent     *view_tree
	left       *view_tree
	top        *view_tree
	right      *view_tree
	bottom     *view_tree
	leaf       *view
	split      float32
	tulib.Rect // updated with 'resize' call
}

func new_view_tree_leaf(parent *view_tree, v *view) *view_tree {
	return &view_tree{
		parent: parent,
		leaf:   v,
	}
}

func (v *view_tree) split_vertically() {
	top := v.leaf
	bottom := new_view(top.ctx, top.buf)
	*v = view_tree{
		parent: v.parent,
		top:    new_view_tree_leaf(v, top),
		bottom: new_view_tree_leaf(v, bottom),
		split:  0.5,
	}
}

func (v *view_tree) split_horizontally() {
	left := v.leaf
	right := new_view(left.ctx, left.buf)
	*v = view_tree{
		parent: v.parent,
		left:   new_view_tree_leaf(v, left),
		right:  new_view_tree_leaf(v, right),
		split:  0.5,
	}
}

func (v *view_tree) draw() {
	if v.leaf != nil {
		v.leaf.draw()
		return
	}

	if v.left != nil {
		v.left.draw()
		v.right.draw()
	} else {
		v.top.draw()
		v.bottom.draw()
	}
}

func (v *view_tree) resize(pos tulib.Rect) {
	v.Rect = pos
	if v.leaf != nil {
		v.leaf.resize(pos.Width, pos.Height)
		return
	}

	if v.left != nil {
		// horizontal split, use 'w'
		w := pos.Width
		if w > 0 {
			// reserve one line for splitter, if we have one line
			w--
		}
		lw := int(float32(w) * v.split)
		rw := w - lw
		v.left.resize(tulib.Rect{pos.X, pos.Y, lw, pos.Height})
		v.right.resize(tulib.Rect{pos.X + lw + 1, pos.Y, rw, pos.Height})
	} else {
		// vertical split, use 'h', no need to reserve one line for
		// splitter, because splitters are part of the buffer's output
		// (their status bars act like a splitter)
		h := pos.Height
		th := int(float32(h) * v.split)
		bh := h - th
		v.top.resize(tulib.Rect{pos.X, pos.Y, pos.Width, th})
		v.bottom.resize(tulib.Rect{pos.X, pos.Y + th, pos.Width, bh})
	}
}

func (v *view_tree) traverse(cb func(*view_tree)) {
	if v.leaf != nil {
		cb(v)
		return
	}

	if v.left != nil {
		v.left.traverse(cb)
		v.right.traverse(cb)
	} else if v.top != nil {
		v.top.traverse(cb)
		v.bottom.traverse(cb)
	}
}

func (v *view_tree) nearest_vsplit() *view_tree {
	v = v.parent
	for v != nil {
		if v.top != nil {
			return v
		}
		v = v.parent
	}
	return nil
}

func (v *view_tree) nearest_hsplit() *view_tree {
	v = v.parent
	for v != nil {
		if v.left != nil {
			return v
		}
		v = v.parent
	}
	return nil
}

func (v *view_tree) one_step() float32 {
	if v.top != nil {
		return 1.0 / float32(v.Height)
	} else if v.left != nil {
		return 1.0 / float32(v.Width-1)
	}
	return 0.0
}

func (v *view_tree) normalize_split() {
	var off int
	if v.top != nil {
		off = int(float32(v.Height) * v.split)
	} else {
		off = int(float32(v.Width-1) * v.split)
	}
	v.split = float32(off) * v.one_step()
}

func (v *view_tree) step_resize(n int) {
	if v.Width <= 1 || v.Height <= 0 {
		// avoid division by zero, result is really bad
		return
	}

	one := v.one_step()
	v.normalize_split()
	v.split += one*float32(n) + (one * 0.5)
	if v.split > 1.0 {
		v.split = 1.0
	}
	if v.split < 0.0 {
		v.split = 0.0
	}
	v.resize(v.Rect)
}

func (v *view_tree) reparent() {
	if v.left != nil {
		v.left.parent = v
		v.right.parent = v
	} else if v.top != nil {
		v.top.parent = v
		v.bottom.parent = v
	}
}

func (v *view_tree) sibling() *view_tree {
	p := v.parent
	if p == nil {
		return nil
	}
	switch {
	case v == p.left:
		return p.right
	case v == p.right:
		return p.left
	case v == p.top:
		return p.bottom
	case v == p.bottom:
		return p.top
	}
	panic("unreachable")
}

func (v *view_tree) first_leaf_node() *view_tree {
	if v.left != nil {
		return v.left.first_leaf_node()
	} else if v.top != nil {
		return v.top.first_leaf_node()
	} else if v.leaf != nil {
		return v
	}
	panic("unreachable")
}
