package executors

import (
	"io"

	"database/btree"
	"database/storage"
)

// TupleReader is a function that reads a tuple from a heap file by TID.
type TupleReader func(tid storage.TID) (storage.Tuple, error)

// BTreeScan is an executor node that scans a B+ tree.
//
// Implements the Node interface:
//   - Next() returns the next record from the tree
//   - Returns io.EOF when all records are scanned
//
// Example:
//
//	tree := btree.NewBTree()
//	tree.Insert(1, storage.TID{PageId: 0, SlotId: 0})
//	tree.Insert(2, storage.TID{PageId: 0, SlotId: 1})
//
//	scan := NewBTreeScan(tree, readTupleFunc)
//	tuple, err := scan.Next()
//	// tuple = {1, "Movie A", "Action"}
type BTreeScan struct {
	tree      *btree.BTree
	leaf      *btree.BTreeNode
	idx       int
	done      bool
	xipList   map[uint64]bool
	readTuple TupleReader
}

// NewBTreeScan creates a new BTreeScan executor node.
func NewBTreeScan(tree *btree.BTree, readTuple TupleReader, xipList map[uint64]bool) *BTreeScan {
	var leaf *btree.BTreeNode
	if tree.Root != nil {
		// find leftmost leaf (start of linked list)
		leaf = tree.Root
		for !leaf.IsLeaf {
			leaf = leaf.Children[0]
		}
	}

	return &BTreeScan{
		tree:      tree,
		leaf:      leaf,
		idx:       0,
		xipList:   xipList,
		readTuple: readTuple,
	}
}
// Next returns the next record from the B+ tree.
//
// Algorithm:
//  1. If current leaf has more keys, read tuple from heap using TID
//  2. If not, follow Next pointer to next leaf
//  3. If no more leaves, return io.EOF
//
// State transitions:
//
//	leaf=[10,20], idx=0 → read tuple from TID[0], idx=1
//	leaf=[10,20], idx=1 → read tuple from TID[1], idx=2
//	leaf=[10,20], idx=2 → leaf=leaf.Next, idx=0
//	leaf=nil → return io.EOF
func (s *BTreeScan) Next() (storage.Tuple, error) {
	if s.done {
		return nil, io.EOF
	}

	for s.leaf != nil {
		if s.idx < len(s.leaf.Keys) {
			// get TID and read tuple from heap
			tid := s.leaf.TIDs[s.idx]
			s.idx++
			tuple, err := s.readTuple(tid)
			if err != nil {
				return nil, err
			}
			return tuple, nil
		}
		// move to next leaf
		s.leaf = s.leaf.Next
		s.idx = 0
	}

	s.done = true
	return nil, io.EOF
}
