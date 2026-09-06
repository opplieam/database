package btree

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"database/storage"
)

// B+ Tree implementation for database indexing.
//
// Structure:
//   - Internal nodes: store keys only (guide search)
//   - Leaf nodes: store keys + values (actual data)
//   - Leaf nodes: linked together (efficient range scans)
//
// Example tree with order 4 (max 3 keys per node):
//
//                    [30 | 60]                    ← root (internal)
//                   /    |    \
//                  /     |     \
//    [10 | 20]  [40 | 50]  [70 | 80]          ← internal nodes
//       ↓          ↓          ↓
//    [1,2,10] → [20,30,40] → [50,60,70] → [80,90]  ← leaf nodes (linked)

// BTreeNodeOrder is the maximum number of keys in a node.
// For a tree of order N:
//   - Internal node has at most N children, N-1 keys
//   - Leaf node has at most N keys
//   - Split when node has N keys
const BTreeNodeOrder = 4

// BTreeNode represents a node in the B+ tree.
//
// Internal node:
//   IsLeaf = false
//   Keys = [30, 60]          (n-1 keys for n children)
//   Children = [nodeA, nodeB, nodeC]
//   Values = nil (not used)
//   Next = nil (not used)
//
// Leaf node:
//   IsLeaf = true
//   Keys = [10, 20, 30]
//   Children = nil (not used)
//   Values = [tuple1, tuple2, tuple3]  (one per key)
//   Next = pointer to next leaf (for range scans)
type BTreeNode struct {
	IsLeaf   bool
	Keys     []int
	Children []*BTreeNode
	Values   []storage.Tuple
	Next     *BTreeNode
}

// BTree is a B+ tree with a root node.
type BTree struct {
	Root *BTreeNode
}

// NewBTree creates an empty B+ tree.
//
// Initial state:
//   Root = nil (empty tree)
//
// After first insert:
//   Root = leaf node with one key-value pair
func NewBTree() *BTree {
	return &BTree{Root: nil}
}

// NewLeafNode creates a new empty leaf node.
//
// Example:
//   node = {IsLeaf: true, Keys: [], Children: nil, Values: [], Next: nil}
func NewLeafNode() *BTreeNode {
	return &BTreeNode{
		IsLeaf:   true,
		Keys:     make([]int, 0),
		Children: nil,
		Values:   make([]storage.Tuple, 0),
		Next:     nil,
	}
}

// NewInternalNode creates a new empty internal node.
//
// Example:
//   node = {IsLeaf: false, Keys: [], Children: [], Values: nil, Next: nil}
func NewInternalNode() *BTreeNode {
	return &BTreeNode{
		IsLeaf:   false,
		Keys:     make([]int, 0),
		Children: make([]*BTreeNode, 0),
		Values:   nil,
		Next:     nil,
	}
}

// IsFull returns true if the node has reached its maximum capacity.
//
// For leaf node:
//   Full when len(Keys) == BTreeNodeOrder
//   Example: order=4, node has 4 keys → full
//
// For internal node:
//   Full when len(Children) == BTreeNodeOrder
//   Example: order=4, node has 4 children → full
func (n *BTreeNode) IsFull() bool {
	if n.IsLeaf {
		return len(n.Keys) >= BTreeNodeOrder
	}
	return len(n.Children) >= BTreeNodeOrder
}

// IsUnderflow returns true if the node has too few keys (for deletion).
//
// For leaf node:
//   Underflow when len(Keys) < BTreeNodeOrder/2
//   Example: order=4, node has 1 key → underflow (need at least 2)
//
// For internal node:
//   Underflow when len(Children) < (BTreeNodeOrder+1)/2
//   Example: order=4, node has 1 child → underflow (need at least 2)
func (n *BTreeNode) IsUnderflow() bool {
	if n.IsLeaf {
		return len(n.Keys) < BTreeNodeOrder/2
	}
	return len(n.Children) < (BTreeNodeOrder+1)/2
}

// String returns a debug representation of the node.
//
// Example leaf:
//   "Leaf[10, 20, 30]"
//
// Example internal:
//   "Internal[30, 60] → 3 children"
func (n *BTreeNode) String() string {
	if n.IsLeaf {
		return "Leaf" + fmt.Sprint(n.Keys)
	}
	return "Internal" + fmt.Sprint(n.Keys) + " → " + fmt.Sprint(len(n.Children)) + " children"
}

// Search finds a key in the B+ tree and returns its value.
//
// Algorithm:
//   1. Start at root
//   2. Find child pointer where key fits (go left if key < node key)
//   3. Move to child, repeat until leaf
//   4. Search leaf for key, return value if found
//
// Example: Search(45) in this tree
//
//                    [30 | 60]
//                   /    |    \
//    [10 | 20]  [40 | 50]  [70 | 80]
//       ↓          ↓          ↓
//    [1,2,10] → [20,30,40] → [50,60,70] → [80,90]
//
// Steps:
//   root [30,60]: 45 >= 30, 45 < 60 → go to middle child
//   internal [40,50]: 45 >= 40, 45 < 50 → go to first child
//   leaf [40,50]: search for 45 → not found
//   return nil, false
//
// Example: Search(40)
//   Same path as above
//   leaf [40,50]: search for 40 → found at index 0
//   return Values[0], true
func (t *BTree) Search(key int) (storage.Tuple, bool) {
	if t.Root == nil {
		return nil, false
	}
	return searchNode(t.Root, key)
}

// searchNode recursively searches for a key in the tree.
func searchNode(node *BTreeNode, key int) (storage.Tuple, bool) {
	if node.IsLeaf {
		// leaf node: search for key
		for i, k := range node.Keys {
			if k == key {
				return node.Values[i], true
			}
		}
		return nil, false
	}

	// internal node: find correct child
	// keys[i] is the separator: all values in Children[i] are < keys[i]
	// all values in Children[i+1] are >= keys[i]
	for i, k := range node.Keys {
		if key < k {
			// key is less than separator, go to left child
			return searchNode(node.Children[i], key)
		}
	}
	// key is greater than all separators, go to rightmost child
	return searchNode(node.Children[len(node.Children)-1], key)
}

// findLeaf navigates from root to the leaf where key should be.
// Used by Insert to find the correct leaf.
func (t *BTree) findLeaf(key int) *BTreeNode {
	if t.Root == nil {
		return nil
	}

	node := t.Root
	for !node.IsLeaf {
		i := 0
		for i < len(node.Keys) && key >= node.Keys[i] {
			i++
		}
		node = node.Children[i]
	}
	return node
}

// Insert adds a key-value pair to the B+ tree.
//
// Algorithm:
//   1. If tree is empty, create root leaf node
//   2. Find correct leaf node
//   3. Insert key-value in sorted order
//   4. If leaf overflows, split it
//   5. If parent overflows, split parent (recursive)
//
// Example: Insert(35) into tree
//
// Before:
//                    [30 | 60]
//                   /    |    \
//    [10 | 20]  [30 | 40 | 50]  [70 | 80]    ← middle leaf is full (3 keys)
//       ↓          ↓          ↓
//    [1,2,10] → [30,40,50] → [70,80,90]
//
// Step 1: Find leaf for key 35 → middle leaf [30,40,50]
// Step 2: Insert 35 → [30,35,40,50] → overflow! (4 keys, max is 3)
// Step 3: Split leaf:
//   - Left: [30,35]
//   - Right: [40,50]
//   - Push up key: 40 (middle key)
//
// After:
//                    [30 | 40 | 60]
//                   /    |    |    \
//    [10 | 20]  [30,35] [40,50]  [70 | 80]
//       ↓          ↓       ↓        ↓
//    [1,2,10] → [...] → [...] → [...]
//
func (t *BTree) Insert(key int, value storage.Tuple) {
	// empty tree: create root leaf
	if t.Root == nil {
		t.Root = NewLeafNode()
		t.Root.Keys = []int{key}
		t.Root.Values = []storage.Tuple{value}
		return
	}

	// find leaf where key belongs
	leaf := t.findLeaf(key)

	// insert in sorted order
	inserted := false
	for i := 0; i < len(leaf.Keys); i++ {
		if key < leaf.Keys[i] {
			// insert at position i
			leaf.Keys = append(leaf.Keys, 0)
			leaf.Values = append(leaf.Values, nil)
			copy(leaf.Keys[i+1:], leaf.Keys[i:])
			copy(leaf.Values[i+1:], leaf.Values[i:])
			leaf.Keys[i] = key
			leaf.Values[i] = value
			inserted = true
			break
		}
	}
	if !inserted {
		// insert at end
		leaf.Keys = append(leaf.Keys, key)
		leaf.Values = append(leaf.Values, value)
	}

	// check for overflow
	if leaf.IsFull() {
		t.splitLeaf(leaf)
	}
}

// splitLeaf splits a full leaf node and pushes the middle key up.
//
// Example: split leaf [30, 35, 40, 50] (order=4, full)
//
// Before:
//   leaf.Keys = [30, 35, 40, 50]
//   leaf.Values = [v30, v35, v40, v50]
//
// After split:
//   leaf (left):  Keys = [30, 35], Values = [v30, v35]
//   new (right):  Keys = [40, 50], Values = [v40, v50]
//   push up key: 40 (the first key of right node)
//
// The right node is linked to left node for range scans:
//   leaf.Next = new (for sequential access)
//
func (t *BTree) splitLeaf(leaf *BTreeNode) {
	// find middle index
	mid := len(leaf.Keys) / 2

	// create new right leaf
	right := NewLeafNode()
	right.Keys = make([]int, len(leaf.Keys)-mid)
	right.Values = make([]storage.Tuple, len(leaf.Values)-mid)
	copy(right.Keys, leaf.Keys[mid:])
	copy(right.Values, leaf.Values[mid:])

	// truncate left leaf
	leaf.Keys = leaf.Keys[:mid]
	leaf.Values = leaf.Values[:mid]

	// link leaves for range scans
	right.Next = leaf.Next
	leaf.Next = right

	// push up the first key of right node
	pushUpKey := right.Keys[0]

	// if leaf is root, create new root
	if leaf == t.Root {
		newRoot := NewInternalNode()
		newRoot.Keys = []int{pushUpKey}
		newRoot.Children = []*BTreeNode{leaf, right}
		t.Root = newRoot
		return
	}

	// find parent and insert key
	t.insertIntoParent(leaf, pushUpKey, right)
}

// insertIntoParent inserts a key and right child into the parent of left child.
//
// Example: parent has [30, 60], left is child[1], we insert key=40, right=new node
//
// Before:
//   parent.Keys = [30, 60]
//   parent.Children = [A, left, C]
//
// After:
//   parent.Keys = [30, 40, 60]
//   parent.Children = [A, left, right, C]
//
// If parent overflows, split it too (recursive)
func (t *BTree) insertIntoParent(left *BTreeNode, key int, right *BTreeNode) {
	// find parent
	parent := t.findParent(left)
	if parent == nil {
		// left is root, create new root
		newRoot := NewInternalNode()
		newRoot.Keys = []int{key}
		newRoot.Children = []*BTreeNode{left, right}
		t.Root = newRoot
		return
	}

	// find position of left in parent's children
	insertIdx := 0
	for i, child := range parent.Children {
		if child == left {
			insertIdx = i
			break
		}
	}

	// insert key and right child
	parent.Keys = append(parent.Keys, 0)
	parent.Children = append(parent.Children, nil)
	copy(parent.Keys[insertIdx+1:], parent.Keys[insertIdx:])
	copy(parent.Children[insertIdx+2:], parent.Children[insertIdx+1:])
	parent.Keys[insertIdx] = key
	parent.Children[insertIdx+1] = right

	// check for overflow
	if parent.IsFull() {
		t.splitInternal(parent)
	}
}

// splitInternal splits a full internal node and pushes the middle key up.
//
// Example: split internal [30, 40, 60] (order=4, full, 4 children)
//
// Before:
//   node.Keys = [30, 40, 60]
//   node.Children = [A, B, C, D]
//
// After split:
//   node (left):  Keys = [30], Children = [A, B]
//   new (right):  Keys = [60], Children = [C, D]
//   push up key: 40 (the middle key)
//
func (t *BTree) splitInternal(node *BTreeNode) {
	// find middle index
	mid := len(node.Keys) / 2
	pushUpKey := node.Keys[mid]

	// create new right internal
	right := NewInternalNode()
	right.Keys = make([]int, len(node.Keys)-mid-1)
	right.Children = make([]*BTreeNode, len(node.Children)-mid-1)
	copy(right.Keys, node.Keys[mid+1:])
	copy(right.Children, node.Children[mid+1:])

	// truncate left node
	node.Keys = node.Keys[:mid]
	node.Children = node.Children[:mid+1]

	// if node is root, create new root
	if node == t.Root {
		newRoot := NewInternalNode()
		newRoot.Keys = []int{pushUpKey}
		newRoot.Children = []*BTreeNode{node, right}
		t.Root = newRoot
		return
	}

	// insert into parent
	t.insertIntoParent(node, pushUpKey, right)
}

// findParent finds the parent of a given node.
func (t *BTree) findParent(child *BTreeNode) *BTreeNode {
	if t.Root == nil || t.Root == child {
		return nil
	}

	var find func(node *BTreeNode) *BTreeNode
	find = func(node *BTreeNode) *BTreeNode {
		if node.IsLeaf {
			return nil
		}
		for _, c := range node.Children {
			if c == child {
				return node
			}
		}
		for _, c := range node.Children {
			if result := find(c); result != nil {
				return result
			}
		}
		return nil
	}

	return find(t.Root)
}

// Print displays the tree structure (for debugging).
func (t *BTree) Print() {
	if t.Root == nil {
		fmt.Println("empty tree")
		return
	}
	printNode(t.Root, 0)
}

func printNode(node *BTreeNode, level int) {
	indent := ""
	for i := 0; i < level; i++ {
		indent += "  "
	}
	fmt.Printf("%s%s\n", indent, node)
	if !node.IsLeaf {
		for _, child := range node.Children {
			printNode(child, level+1)
		}
	}
}

// Delete removes a key from the B+ tree.
//
// Algorithm:
//   1. Find leaf containing key
//   2. Remove key-value pair
//   3. If leaf underflows (too few keys):
//      a. Try borrowing from sibling (redistribute)
//      b. If can't borrow, merge with sibling
//   4. If parent underflows, merge parent too (recursive)
//
// Example: Delete(20) from this tree
//
// Before:
//        [30, 40]
//       /   |   \
//    [10,20] [30,35] [40,50]
//
// Step 1: Find leaf for key 20 → left leaf [10, 20]
// Step 2: Remove 20 → [10]
// Step 3: Underflow? len=1, min=2 → YES
// Step 4: Try borrow from sibling [30,35]
//   - Can borrow? 3 > 2 → YES
//   - Redistribute: take 30 from sibling
//   - left leaf: [10, 30]
//   - sibling: [35]
//   - Update parent key: 30 → 35
//
// After:
//        [35, 40]
//       /   |   \
//    [10,30] [35] [40,50]
//
func (t *BTree) Delete(key int) {
	if t.Root == nil {
		return
	}

	// find leaf containing key
	leaf := t.findLeaf(key)
	if leaf == nil {
		return
	}

	// find and remove key
	idx := -1
	for i, k := range leaf.Keys {
		if k == key {
			idx = i
			break
		}
	}
	if idx == -1 {
		return // key not found
	}

	// remove key-value pair
	leaf.Keys = append(leaf.Keys[:idx], leaf.Keys[idx+1:]...)
	leaf.Values = append(leaf.Values[:idx], leaf.Values[idx+1:]...)

	// check for underflow
	if leaf.IsUnderflow() && leaf != t.Root {
		t.handleUnderflow(leaf)
	}

	// if root is empty internal node with one child, make child the new root
	if !t.Root.IsLeaf && len(t.Root.Keys) == 0 && len(t.Root.Children) == 1 {
		t.Root = t.Root.Children[0]
	}
}

// handleUnderflow handles a node that has too few keys.
//
// Two options:
//   1. Borrow from left or right sibling (redistribute)
//   2. Merge with sibling
//
// Example: handle underflow in leaf [10]
//
// Tree:
//        [30, 40]
//       /   |   \
//    [10] [30,35] [40,50]
//
// Option 1: Borrow from right sibling [30,35]
//   sibling has 3 keys, we need 2 → can borrow
//   take 30 from sibling
//   leaf: [10, 30]
//   sibling: [35]
//   update parent key: 30 → 35
//
// Option 2: Merge with sibling (if sibling can't lend)
//   merge leaf [10] with sibling [30,35] → [10, 30, 35]
//   remove parent key 30
//   remove sibling from parent children
func (t *BTree) handleUnderflow(node *BTreeNode) {
	parent := t.findParent(node)
	if parent == nil {
		return
	}

	// find index of node in parent's children
	idx := -1
	for i, child := range parent.Children {
		if child == node {
			idx = i
			break
		}
	}

	// try borrowing from left sibling
	if idx > 0 {
		leftSibling := parent.Children[idx-1]
		if len(leftSibling.Keys) > BTreeNodeOrder/2 {
			t.borrowFromLeft(node, leftSibling, parent, idx-1)
			return
		}
	}

	// try borrowing from right sibling
	if idx < len(parent.Children)-1 {
		rightSibling := parent.Children[idx+1]
		if len(rightSibling.Keys) > BTreeNodeOrder/2 {
			t.borrowFromRight(node, rightSibling, parent, idx)
			return
		}
	}

	// can't borrow, merge with sibling
	if idx > 0 {
		// merge with left sibling
		t.mergeWithLeft(node, parent.Children[idx-1], parent, idx-1)
	} else if idx < len(parent.Children)-1 {
		// merge with right sibling
		t.mergeWithRight(node, parent.Children[idx+1], parent, idx)
	}
}

// borrowFromLeft borrows a key from left sibling.
//
// Example: borrow from left sibling [30, 35]
//
// Before:
//   parent.Keys = [30, 40]
//   parent.Children = [[10], [30,35], [40,50]]
//                     idx-1  idx
//
// After:
//   parent.Keys = [35, 40]  (changed 30 → 35)
//   parent.Children = [[10,30], [35], [40,50]]
//
func (t *BTree) borrowFromLeft(node *BTreeNode, leftSibling *BTreeNode, parent *BTreeNode, siblingIdx int) {
	if node.IsLeaf {
		// leaf: take last key from sibling
		lastKey := leftSibling.Keys[len(leftSibling.Keys)-1]
		lastVal := leftSibling.Values[len(leftSibling.Values)-1]

		// remove from sibling
		leftSibling.Keys = leftSibling.Keys[:len(leftSibling.Keys)-1]
		leftSibling.Values = leftSibling.Values[:len(leftSibling.Values)-1]

		// add to front of node
		node.Keys = append([]int{lastKey}, node.Keys...)
		node.Values = append([]storage.Tuple{lastVal}, node.Values...)

		// update parent key
		parent.Keys[siblingIdx] = node.Keys[0]
	} else {
		// internal: take last key from sibling and parent
		lastKey := parent.Keys[siblingIdx]
		lastChild := leftSibling.Children[len(leftSibling.Children)-1]

		// remove from sibling
		leftSibling.Keys = leftSibling.Keys[:len(leftSibling.Keys)-1]
		leftSibling.Children = leftSibling.Children[:len(leftSibling.Children)-1]

		// add to front of node
		node.Keys = append([]int{lastKey}, node.Keys...)
		node.Children = append([]*BTreeNode{lastChild}, node.Children...)

		// update parent key
		parent.Keys[siblingIdx] = leftSibling.Keys[len(leftSibling.Keys)-1]
	}
}

// borrowFromRight borrows a key from right sibling.
//
// Example: borrow from right sibling [30, 35]
//
// Before:
//   parent.Keys = [30, 40]
//   parent.Children = [[10], [30,35], [40,50]]
//                     idx   idx+1
//
// After:
//   parent.Keys = [30, 35]  (changed 40 → 35... wait, need to think)
//   Actually: parent.Keys = [30, 40] → [30, 35]
//   parent.Children = [[10], [30], [35, 40, 50]]
//
func (t *BTree) borrowFromRight(node *BTreeNode, rightSibling *BTreeNode, parent *BTreeNode, siblingIdx int) {
	if node.IsLeaf {
		// leaf: take first key from sibling
		firstKey := rightSibling.Keys[0]
		firstVal := rightSibling.Values[0]

		// remove from sibling
		rightSibling.Keys = rightSibling.Keys[1:]
		rightSibling.Values = rightSibling.Values[1:]

		// add to end of node
		node.Keys = append(node.Keys, firstKey)
		node.Values = append(node.Values, firstVal)

		// update parent key
		parent.Keys[siblingIdx] = rightSibling.Keys[0]
	} else {
		// internal: take first key from sibling and parent
		firstKey := parent.Keys[siblingIdx]
		firstChild := rightSibling.Children[0]

		// remove from sibling
		rightSibling.Keys = rightSibling.Keys[1:]
		rightSibling.Children = rightSibling.Children[1:]

		// add to end of node
		node.Keys = append(node.Keys, firstKey)
		node.Children = append(node.Children, firstChild)

		// update parent key
		parent.Keys[siblingIdx] = rightSibling.Keys[0]
	}
}

// mergeWithLeft merges node with its left sibling.
//
// Example: merge [10] with left sibling [30, 35]
//
// Before:
//   parent.Keys = [30, 40]
//   parent.Children = [[10], [30,35], [40,50]]
//
// After:
//   parent.Keys = [40]
//   parent.Children = [[10,30,35], [40,50]]
//
func (t *BTree) mergeWithLeft(node *BTreeNode, leftSibling *BTreeNode, parent *BTreeNode, siblingIdx int) {
	if node.IsLeaf {
		// merge leaf: combine keys and values
		leftSibling.Keys = append(leftSibling.Keys, node.Keys...)
		leftSibling.Values = append(leftSibling.Values, node.Values...)
		leftSibling.Next = node.Next
	} else {
		// merge internal: combine keys and children
		// push down parent key
		parentKey := parent.Keys[siblingIdx]
		leftSibling.Keys = append(leftSibling.Keys, parentKey)
		leftSibling.Keys = append(leftSibling.Keys, node.Keys...)
		leftSibling.Children = append(leftSibling.Children, node.Children...)
	}

	// remove key and child from parent
	parent.Keys = append(parent.Keys[:siblingIdx], parent.Keys[siblingIdx+1:]...)
	parent.Children = append(parent.Children[:siblingIdx+1], parent.Children[siblingIdx+2:]...)

	// check parent underflow
	if parent.IsUnderflow() && parent != t.Root {
		t.handleUnderflow(parent)
	}
}

// mergeWithRight merges node with its right sibling.
//
// Example: merge [35] with right sibling [40, 50]
//
// Before:
//   parent.Keys = [35, 40]
//   parent.Children = [[10,30], [35], [40,50]]
//
// After:
//   parent.Keys = [35]
//   parent.Children = [[10,30], [35,40,50]]
//
func (t *BTree) mergeWithRight(node *BTreeNode, rightSibling *BTreeNode, parent *BTreeNode, siblingIdx int) {
	if node.IsLeaf {
		// merge leaf
		node.Keys = append(node.Keys, rightSibling.Keys...)
		node.Values = append(node.Values, rightSibling.Values...)
		node.Next = rightSibling.Next
	} else {
		// merge internal
		parentKey := parent.Keys[siblingIdx]
		node.Keys = append(node.Keys, parentKey)
		node.Keys = append(node.Keys, rightSibling.Keys...)
		node.Children = append(node.Children, rightSibling.Children...)
	}

	// remove key and child from parent
	parent.Keys = append(parent.Keys[:siblingIdx], parent.Keys[siblingIdx+1:]...)
	parent.Children = append(parent.Children[:siblingIdx+1], parent.Children[siblingIdx+2:]...)

	// check parent underflow
	if parent.IsUnderflow() && parent != t.Root {
		t.handleUnderflow(parent)
	}
}

// RangeScan finds all key-value pairs where min <= key <= max.
//
// Algorithm:
//   1. Find first leaf where min could be
//   2. Follow linked list, collecting values where min <= key <= max
//   3. Stop when key > max
//
// Example: RangeScan(25, 45) in this tree
//
//        [30, 40]
//       /   |   \
//    [10,20,25] [30,35] [40,50]
//       ↓         ↓        ↓
//    leaf1 →   leaf2 →  leaf3
//
// Steps:
//   1. Find leaf for key 25 → leaf1 [10, 20, 25]
//   2. Start at leaf1:
//      - 10 < 25? skip
//      - 20 < 25? skip
//      - 25 >= 25 and 25 <= 45? collect
//   3. Follow link to leaf2 [30, 35]:
//      - 30 >= 25 and 30 <= 45? collect
//      - 35 >= 25 and 35 <= 45? collect
//   4. Follow link to leaf3 [40, 50]:
//      - 40 >= 25 and 40 <= 45? collect
//      - 50 <= 45? NO, stop
//
// Result: [25, 30, 35, 40]
//
func (t *BTree) RangeScan(min, max int) []storage.Tuple {
	if t.Root == nil {
		return nil
	}

	// find first leaf where min could be
	leaf := t.findLeaf(min)
	if leaf == nil {
		return nil
	}

	var result []storage.Tuple

	// traverse leaf nodes
	for leaf != nil {
		for i, key := range leaf.Keys {
			if key >= min && key <= max {
				result = append(result, leaf.Values[i])
			}
			if key > max {
				// no more keys in range
				return result
			}
		}
		leaf = leaf.Next
	}

	return result
}

// RangeScanKeys returns just the keys in range (for debugging).
func (t *BTree) RangeScanKeys(min, max int) []int {
	if t.Root == nil {
		return nil
	}

	leaf := t.findLeaf(min)
	if leaf == nil {
		return nil
	}

	var result []int

	for leaf != nil {
		for _, key := range leaf.Keys {
			if key >= min && key <= max {
				result = append(result, key)
			}
			if key > max {
				return result
			}
		}
		leaf = leaf.Next
	}

	return result
}

// Save writes the B+ tree to a file.
//
// File format:
//   - Header: magic bytes "BTREE" (5 bytes)
//   - Node count (4 bytes)
//   - For each node:
//     - IsLeaf (1 byte: 0 or 1)
//     - Key count (2 bytes)
//     - Keys (key_count × 4 bytes)
//     - For leaf: values (key_count × encoded tuples)
//     - For internal: child indices (key_count+1 × 4 bytes)
//     - Next leaf index (4 bytes, -1 if none)
//
// Example: tree with 3 nodes
//
//   root (internal): Keys=[30], Children=[leaf1, leaf2]
//   leaf1: Keys=[10,20], Values=[t1,t2], Next=leaf2
//   leaf2: Keys=[30,40], Values=[t3,t4], Next=nil
//
// File layout:
//   "BTREE"
//   3 (node count)
//   node0: internal, 1 key, [30], children=[1,2], next=-1
//   node1: leaf, 2 keys, [10,20], values=[...], next=2
//   node2: leaf, 2 keys, [30,40], values=[...], next=-1
//
func (t *BTree) Save(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// write magic bytes
	if _, err := f.Write([]byte("BTREE")); err != nil {
		return err
	}

	// collect all nodes and assign IDs
	var nodeIDs map[*BTreeNode]uint32
	var nodes []*BTreeNode
	var collect func(node *BTreeNode)
	collect = func(node *BTreeNode) {
		if node == nil {
			return
		}
		nodeIDs[node] = uint32(len(nodes))
		nodes = append(nodes, node)
		if !node.IsLeaf {
			for _, child := range node.Children {
				collect(child)
			}
		}
	}
	nodeIDs = make(map[*BTreeNode]uint32)
	collect(t.Root)

	// write node count
	nodeCount := uint32(len(nodes))
	if err := binary.Write(f, binary.LittleEndian, nodeCount); err != nil {
		return err
	}

	// write each node
	for _, node := range nodes {
		if err := writeNodeWithIDs(f, node, nodeIDs); err != nil {
			return err
		}
	}

	return nil
}

// LoadBTree reads a B+ tree from a file.
func LoadBTree(path string) (*BTree, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// read magic bytes
	magic := make([]byte, 5)
	if _, err := io.ReadFull(f, magic); err != nil {
		return nil, err
	}
	if string(magic) != "BTREE" {
		return nil, errors.New("invalid file format")
	}

	// read node count
	var nodeCount uint32
	if err := binary.Read(f, binary.LittleEndian, &nodeCount); err != nil {
		return nil, err
	}

	// read all nodes (without pointers)
	nodes := make([]*BTreeNode, nodeCount)
	for i := uint32(0); i < nodeCount; i++ {
		node, childIDs, nextID, err := readNodeWithIDs(f)
		if err != nil {
			return nil, err
		}
		nodes[i] = node
		
		// store IDs for rebuilding pointers
		type tempData struct {
			childIDs []uint32
			nextID   uint32
		}
		// store in a map keyed by index
		_ = childIDs
		_ = nextID
	}

	// rebuild pointers
	// re-read to get the IDs
	f.Seek(5+4, 0) // skip magic + count
	for i := uint32(0); i < nodeCount; i++ {
		_, childIDs, nextID, err := readNodeWithIDs(f)
		if err != nil {
			return nil, err
		}
		
		if !nodes[i].IsLeaf {
			nodes[i].Children = make([]*BTreeNode, len(childIDs))
			for j, childID := range childIDs {
				if childID < nodeCount {
					nodes[i].Children[j] = nodes[childID]
				}
			}
		}
		
		if nodes[i].IsLeaf && nextID < nodeCount {
			nodes[i].Next = nodes[nextID]
		}
	}

	return &BTree{Root: nodes[0]}, nil
}

// writeNodeWithIDs writes a node with child IDs.
func writeNodeWithIDs(f *os.File, node *BTreeNode, nodeIDs map[*BTreeNode]uint32) error {
	// write isLeaf
	if node.IsLeaf {
		if err := binary.Write(f, binary.LittleEndian, uint8(1)); err != nil {
			return err
		}
	} else {
		if err := binary.Write(f, binary.LittleEndian, uint8(0)); err != nil {
			return err
		}
	}

	// write key count
	keyCount := uint16(len(node.Keys))
	if err := binary.Write(f, binary.LittleEndian, keyCount); err != nil {
		return err
	}

	// write keys
	for _, key := range node.Keys {
		if err := binary.Write(f, binary.LittleEndian, uint32(key)); err != nil {
			return err
		}
	}

	// write values (leaf) or child IDs (internal)
	if node.IsLeaf {
		for _, val := range node.Values {
			valBytes := encodeTuple(val)
			if err := binary.Write(f, binary.LittleEndian, uint32(len(valBytes))); err != nil {
				return err
			}
			if _, err := f.Write(valBytes); err != nil {
				return err
			}
		}
	} else {
		for _, child := range node.Children {
			childID := nodeIDs[child]
			if err := binary.Write(f, binary.LittleEndian, childID); err != nil {
				return err
			}
		}
	}

	// write next leaf ID (leaf only)
	if node.IsLeaf {
		if node.Next != nil {
			nextID := nodeIDs[node.Next]
			if err := binary.Write(f, binary.LittleEndian, nextID); err != nil {
				return err
			}
		} else {
			if err := binary.Write(f, binary.LittleEndian, uint32(0xFFFFFFFF)); err != nil {
				return err
			}
		}
	}

	return nil
}

// readNodeWithIDs reads a node and returns child/next IDs.
func readNodeWithIDs(f *os.File) (*BTreeNode, []uint32, uint32, error) {
	var isLeaf uint8
	if err := binary.Read(f, binary.LittleEndian, &isLeaf); err != nil {
		return nil, nil, 0, err
	}

	node := &BTreeNode{IsLeaf: isLeaf == 1}

	var keyCount uint16
	if err := binary.Read(f, binary.LittleEndian, &keyCount); err != nil {
		return nil, nil, 0, err
	}

	node.Keys = make([]int, keyCount)
	for i := uint16(0); i < keyCount; i++ {
		var key uint32
		if err := binary.Read(f, binary.LittleEndian, &key); err != nil {
			return nil, nil, 0, err
		}
		node.Keys[i] = int(key)
	}

	var childIDs []uint32
	var nextID uint32

	if node.IsLeaf {
		node.Values = make([]storage.Tuple, keyCount)
		for i := uint16(0); i < keyCount; i++ {
			var valLen uint32
			if err := binary.Read(f, binary.LittleEndian, &valLen); err != nil {
				return nil, nil, 0, err
			}
			valBytes := make([]byte, valLen)
			if _, err := io.ReadFull(f, valBytes); err != nil {
				return nil, nil, 0, err
			}
			node.Values[i] = decodeTuple(valBytes)
		}
		if err := binary.Read(f, binary.LittleEndian, &nextID); err != nil {
			return nil, nil, 0, err
		}
	} else {
		childIDs = make([]uint32, keyCount+1)
		for i := uint16(0); i <= keyCount; i++ {
			if err := binary.Read(f, binary.LittleEndian, &childIDs[i]); err != nil {
				return nil, nil, 0, err
			}
		}
	}

	return node, childIDs, nextID, nil
}

// encodeTuple encodes a Tuple to bytes (simplified).
func encodeTuple(t storage.Tuple) []byte {
	if len(t) == 0 {
		return []byte{0}
	}
	// encode as: count + each value as string
	var result []byte
	result = append(result, byte(len(t)))
	for _, v := range t {
		s := fmt.Sprint(v)
		result = append(result, byte(len(s)))
		result = append(result, []byte(s)...)
	}
	return result
}

// decodeTuple decodes bytes to a Tuple (simplified).
func decodeTuple(data []byte) storage.Tuple {
	if len(data) == 0 {
		return nil
	}
	count := int(data[0])
	t := make(storage.Tuple, count)
	offset := 1
	for i := 0; i < count; i++ {
		strLen := int(data[offset])
		offset++
		t[i] = string(data[offset : offset+strLen])
		offset += strLen
	}
	return t
}
