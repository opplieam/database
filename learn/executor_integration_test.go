package learn

import (
	"os"
	"strings"
	"testing"

	"database/executors"
	"database/storage"

	"github.com/stretchr/testify/suite"
)

type ExecutorSuite struct {
	suite.Suite
	filename string
}

func (s *ExecutorSuite) SetupTest() {
	s.filename = "test_executor.data"
	err := storage.WriteMoviesPages(s.filename, testMovies)
	s.Require().NoError(err)
}

func (s *ExecutorSuite) TearDownTest() {
	os.Remove(s.filename)
}

func (s *ExecutorSuite) TestExecutorComposition() {
	s.T().Logf("Step 1: Create HeapFileScan (legacy mode - reading raw MovieRecord)")
	scan, err := executors.NewHeapFileScan(s.filename, nil)
	s.Require().NoError(err)
	defer scan.Close()

	s.T().Logf("Step 2: Selection - filter Comedy movies")
	filtered := executors.NewSelection(scan, func(t storage.Tuple) bool {
		genres := t[2].(string)
		return strings.Contains(genres, "Comedy")
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
	s.Equal(2, len(result), "should return 2 Comedy movies")

	for _, r := range result {
		title := r[0].(string)
		s.T().Logf("  - %s", title)
	}
}

func TestExecutorSuite(t *testing.T) {
	suite.Run(t, new(ExecutorSuite))
}
