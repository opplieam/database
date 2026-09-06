package executors

import (
	"io"
	"database/btree"
	"database/storage"
)

// BTreeScan is an executor node that scans a B+ tree.
//
// Implements the Node interface:
//   - Next() returns the next record from the tree
//   - Returns io.EOF when all records are scanned
//
// Example:
//   tree := btree.NewBTree()
//   tree.Insert(1, storage.Tuple{1, "Movie A", "Action"})
//   tree.Insert(2, storage.Tuple{2, "Movie B", "Comedy"})
//
//   scan := NewBTreeScan(tree)
//   tuple, err := scan.Next()
//   // tuple = {1, "Movie A", "Action"}
//
//   tuple, err = scan.Next()
//   // tuple = {2, "Movie B", "Comedy"}
//
//   tuple, err = scan.Next()
//   // tuple = nil, err = io.EOF
type BTreeScan struct {
	tree   *btree.BTree
	leaf   *btree.BTreeNode  // current leaf node
	idx    int         // index within current leaf
	done   bool
}

// NewBTreeScan creates a new BTreeScan executor node.
func NewBTreeScan(tree *btree.BTree) *BTreeScan {
	var leaf *btree.BTreeNode
	if tree.Root != nil {
		// find leftmost leaf (start of linked list)
		leaf = tree.Root
		for !leaf.IsLeaf {
			leaf = leaf.Children[0]
		}
	}

	return &BTreeScan{
		tree: tree,
		leaf: leaf,
		idx:  0,
	}
}

// Next returns the next record from the B+ tree.
//
// Algorithm:
//   1. If current leaf has more keys, return current record
//   2. If not, follow Next pointer to next leaf
//   3. If no more leaves, return io.EOF
//
// State transitions:
//   leaf=[10,20], idx=0 → return 10, idx=1
//   leaf=[10,20], idx=1 → return 20, idx=2
//   leaf=[10,20], idx=2 → leaf=leaf.Next, idx=0
//   leaf=nil → return io.EOF
func (s *BTreeScan) Next() (storage.Tuple, error) {
	if s.done {
		return nil, io.EOF
	}

	for s.leaf != nil {
		if s.idx < len(s.leaf.Keys) {
			// return current record
			value := s.leaf.Values[s.idx]
			s.idx++
			return value, nil
		}
		// move to next leaf
		s.leaf = s.leaf.Next
		s.idx = 0
	}

	s.done = true
	return nil, io.EOF
}
