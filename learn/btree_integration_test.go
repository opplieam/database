package learn

import (
	"os"
	"testing"

	"database/btree"
	"database/executors"
	"database/storage"

	"github.com/stretchr/testify/suite"
)

type BTreeSuite struct {
	suite.Suite
	tree     *btree.BTree
	filename string
}

func (s *BTreeSuite) SetupSuite() {
	s.T().Logf("Building B+ tree from test data")
	s.tree = btree.NewBTree()
	for _, m := range testMovies {
		s.tree.Insert(int(m.MovieId), storage.TID{PageId: 0, SlotId: uint16(m.MovieId)})
	}
	s.filename = "test_btree.data"
}

func (s *BTreeSuite) TearDownSuite() {
	os.Remove(s.filename)
}

func (s *BTreeSuite) TestSearch() {
	s.T().Logf("Search for movie ID 1")
	tid, ok := s.tree.Search(1)
	s.Require().True(ok, "should find movie ID 1")
	s.Equal(uint32(0), tid.PageId, "movie ID 1 should have PageId 0")
	s.Equal(uint16(1), tid.SlotId, "movie ID 1 should have SlotId 1")

	s.T().Logf("Search for movie ID 5")
	tid, ok = s.tree.Search(5)
	s.Require().True(ok, "should find movie ID 5")
	s.Equal(uint16(5), tid.SlotId, "movie ID 5 should have SlotId 5")

	s.T().Logf("Search for non-existent movie")
	_, ok = s.tree.Search(999)
	s.False(ok, "should not find movie ID 999")
}

func (s *BTreeSuite) TestRangeScan() {
	s.T().Logf("Range scan IDs 3-7")
	keys := s.tree.RangeScanKeys(3, 7)
	s.Equal([]int{3, 4, 5, 6, 7}, keys, "should find movies 3-7")

	s.T().Logf("Range scan IDs 8-12")
	keys = s.tree.RangeScanKeys(8, 12)
	s.Equal([]int{8, 9, 10}, keys, "should find movies 8-10 (11, 12 don't exist)")
}

func (s *BTreeSuite) TestPersistence() {
	s.T().Logf("Save tree to file")
	err := s.tree.Save(s.filename)
	s.Require().NoError(err)

	s.T().Logf("Load tree from file")
	loadedTree, err := btree.LoadBTree(s.filename)
	s.Require().NoError(err)

	s.T().Logf("Search loaded tree")
	tid, ok := loadedTree.Search(1)
	s.Require().True(ok, "should find movie ID 1 in loaded tree")
	s.Equal(uint32(0), tid.PageId, "loaded tree should have correct PageId")
	s.Equal(uint16(1), tid.SlotId, "loaded tree should have correct SlotId")

	s.T().Logf("Range scan loaded tree")
	keys := loadedTree.RangeScanKeys(3, 7)
	s.Equal([]int{3, 4, 5, 6, 7}, keys, "loaded tree should support range scan")
}

func (s *BTreeSuite) TestBTreeScanComposition() {
	s.T().Logf("Step 1: Create BTreeScan with mock tuple reader")

	// Mock tuple reader that returns TxRecord based on TID
	readTuple := func(tid storage.TID) (storage.TxRecord, error) {
		// Find movie by SlotId (which we used as MovieId)
		for _, m := range testMovies {
			if m.MovieId == uint32(tid.SlotId) {
				return storage.TxRecord{
					TxMin: 0,
					TxMax: 0,
					CID:   0,
					Data:  storage.Tuple{m.MovieId, m.Title, m.Genres},
				}, nil
			}
		}
		return storage.TxRecord{}, nil
	}

	scan := executors.NewBTreeScan(s.tree, readTuple, nil)

	s.T().Logf("Step 2: Selection - filter movies with ID > 5")
	filtered := executors.NewSelection(scan, func(t storage.Tuple) bool {
		return t[0].(uint32) > 5
	})

	s.T().Logf("Step 3: Projection - pick title only")
	projected := executors.NewProjection(filtered, func(t storage.Tuple) storage.Tuple {
		return storage.Tuple{t[1]}
	})

	s.T().Logf("Step 4: Limit - first 3 results")
	limited := executors.NewLimit(projected, 3)

	s.T().Logf("Step 5: Run query")
	result, err := executors.Run(limited)
	s.Require().NoError(err)

	s.T().Logf("Step 6: Verify results")
	s.Equal(3, len(result), "should return 3 movies")

	// Movies with ID > 5: Sudden Death(6), GoldenEye(7), American President(8), Dracula(9), Balto(10)
	// After limit 3: Sudden Death, GoldenEye, American President
	expectedTitles := []string{"Sudden Death", "GoldenEye", "American President"}
	for i, r := range result {
		title := r[0].(string)
		s.T().Logf("  - %s", title)
		s.Equal(expectedTitles[i], title, "movie %d should be %s", i, expectedTitles[i])
	}
}

func TestBTreeSuite(t *testing.T) {
	suite.Run(t, new(BTreeSuite))
}
