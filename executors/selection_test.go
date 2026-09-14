package executors

import (
	"testing"

	"database/storage"

	"github.com/stretchr/testify/assert"
)

func TestSelection(t *testing.T) {
	scan := NewMemoryScan(testBirds, nil)
	filtered := NewSelection(scan, func(t storage.Tuple) bool {
		return !t[3].(bool) // non-US birds
	})

	result, err := Run(filtered)

	assert.NoError(t, err)
	assert.Equal(t, 3, len(result), "should find 3 non-US birds")

	// Verify all results are non-US
	for _, r := range result {
		assert.False(t, r[3].(bool), "bird should be non-US: %v", r[0])
	}
}
