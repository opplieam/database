package executors

import (
	"testing"

	"database/storage"

	"github.com/stretchr/testify/assert"
)

func TestSort(t *testing.T) {
	scan := NewMemoryScan(testBirds, nil)
	sorted := NewSort(scan, func(t storage.Tuple) any {
		return t[2] // sort by weight
	}, true) // descending

	result, err := Run(sorted)

	assert.NoError(t, err)
	assert.Equal(t, len(testBirds), len(result))

	// Verify order: Ostrich (104) > Emperor Penguin (23) > Wandering Albatross (8.5)
	assert.Equal(t, "ostric1", result[0][0], "heaviest should be Ostrich")
	assert.Equal(t, "emppen1", result[1][0], "second heaviest should be Emperor Penguin")
	assert.Equal(t, "wanalb", result[2][0], "third heaviest should be Wandering Albatross")
}
