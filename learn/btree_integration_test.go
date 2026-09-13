package learn

import (
	"fmt"
	"os"
	"testing"

	"database/btree"
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
		s.tree.Insert(int(m.MovieId), storage.Tuple{m.MovieId, m.Title, m.Genres})
	}
	s.filename = "test_btree.data"
}

func (s *BTreeSuite) TearDownSuite() {
	os.Remove(s.filename)
}

func (s *BTreeSuite) TestSearch() {
	s.T().Logf("Search for movie ID 1")
	val, ok := s.tree.Search(1)
	s.Require().True(ok, "should find movie ID 1")
	s.Equal("Toy Story", val[1], "movie ID 1 should be Toy Story")

	s.T().Logf("Search for movie ID 5")
	val, ok = s.tree.Search(5)
	s.Require().True(ok, "should find movie ID 5")
	s.Equal("Tom and Huck", val[1], "movie ID 5 should be Tom and Huck")

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
	val, ok := loadedTree.Search(1)
	s.Require().True(ok, "should find movie ID 1 in loaded tree")

	// Note: decoded tuples have strings (serialization converts types to strings)
	s.Equal(fmt.Sprintf("%d", 1), val[0], "loaded tree should have correct key")

	s.T().Logf("Range scan loaded tree")
	keys := loadedTree.RangeScanKeys(3, 7)
	s.Equal([]int{3, 4, 5, 6, 7}, keys, "loaded tree should support range scan")
}

func TestBTreeSuite(t *testing.T) {
	suite.Run(t, new(BTreeSuite))
}
