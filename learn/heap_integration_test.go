package learn

import (
	"os"
	"testing"

	"database/executors"
	"database/storage"

	"github.com/stretchr/testify/suite"
)

type HeapSuite struct {
	suite.Suite
	filename string
}

func (s *HeapSuite) SetupTest() {
	s.filename = "test_heap.data"
	err := storage.WriteMoviesPages(s.filename, testMovies)
	s.Require().NoError(err)
}

func (s *HeapSuite) TearDownTest() {
	os.Remove(s.filename)
}

func (s *HeapSuite) TestHeapFileScanIO() {
	s.T().Logf("Step 1: Open HeapFileScan")
	scan, err := executors.NewHeapFileScan(s.filename)
	s.Require().NoError(err)
	defer scan.Close()

	s.T().Logf("Step 2: Read all records")
	result, err := executors.Run(scan)
	s.Require().NoError(err)

	s.T().Logf("Step 3: Verify count")
	s.Equal(len(testMovies), len(result), "should read all records")

	s.T().Logf("Step 4: Verify first record")
	s.Equal("Toy Story", result[0][1], "first movie should be Toy Story")
}

func TestHeapSuite(t *testing.T) {
	suite.Run(t, new(HeapSuite))
}
