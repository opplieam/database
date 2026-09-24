package executors

import (
	"io"

	"database/btree"
	"database/storage"
)

// TupleReader is a function that reads a TxRecord from a heap file by TID.
//
// In MVCC mode, returns the full TxRecord with metadata (TxMin, TxMax, CID).
// In legacy mode, wraps Tuple in TxRecord with TxMin=0, TxMax=0, CID=0.
type TupleReader func(tid storage.TID) (storage.TxRecord, error)

// BTreeScan is an executor node that scans a B+ tree.
//
// Implements the Node interface:
//   - Next() returns the next record from the tree
//   - Returns io.EOF when all records are scanned
//
// When a TransactionContext is provided:
//   - Checks visibility using TxRecord.Visible()
//   - Only returns tuples visible to the current transaction
//
// When ctx is nil (legacy mode):
//   - Returns all tuples without MVCC filtering
//
// Example:
//
//	tree := btree.NewBTree()
//	tree.Insert(1, storage.TID{PageId: 0, SlotId: 0})
//	tree.Insert(2, storage.TID{PageId: 0, SlotId: 1})
//
//	scan := NewBTreeScan(tree, readTupleFunc, nil)
//	tuple, err := scan.Next()
//	// tuple = {1, "Movie A", "Action"}
type BTreeScan struct {
	tree      *btree.BTree
	leaf      *btree.BTreeNode
	idx       int
	done      bool
	ctx       *TransactionContext
	readTuple TupleReader
}

// NewBTreeScan creates a new BTreeScan executor node.
func NewBTreeScan(tree *btree.BTree, readTuple TupleReader, ctx *TransactionContext) *BTreeScan {
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
		ctx:       ctx,
		readTuple: readTuple,
	}
}

// Next returns the next visible record from the B+ tree.
//
// Algorithm:
//  1. If current leaf has more keys, read TxRecord from heap using TID
//  2. Check visibility if MVCC mode
//  3. If not visible, skip and continue
//  4. If not, follow Next pointer to next leaf
//  5. If no more leaves, return io.EOF
func (s *BTreeScan) Next() (storage.Tuple, error) {
	if s.done {
		return nil, io.EOF
	}

	for s.leaf != nil {
		for s.idx < len(s.leaf.Keys) {
			// get TID and read TxRecord from heap
			tid := s.leaf.TIDs[s.idx]
			s.idx++

			txRec, err := s.readTuple(tid)
			if err != nil {
				return nil, err
			}

			// check visibility if MVCC mode
			if s.ctx != nil {
				if !txRec.Visible(s.ctx.Snapshot, s.ctx.Clog, s.ctx.XipList) {
					continue // skip invisible record
				}
			}

			return txRec.Data, nil
		}
		// move to next leaf
		s.leaf = s.leaf.Next
		s.idx = 0
	}

	s.done = true
	return nil, io.EOF
}
