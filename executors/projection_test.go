package executors

import (
	"testing"

	"database/storage"

	"github.com/stretchr/testify/assert"
)

func TestProjection(t *testing.T) {
	scan := NewMemoryScan(testBirds)
	projected := NewProjection(scan, func(t storage.Tuple) storage.Tuple {
		return storage.Tuple{t[0], t[1]} // code, name only
	})

	result, err := Run(projected)

	assert.NoError(t, err)
	assert.Equal(t, len(testBirds), len(result))

	// Verify each result has only 2 fields
	for _, r := range result {
		assert.Equal(t, 2, len(r), "projected tuple should have 2 fields")
	}

	// Verify first tuple
	assert.Equal(t, "amerob", result[0][0])
	assert.Equal(t, "American Robin", result[0][1])
}
