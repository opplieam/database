package executors

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLimit(t *testing.T) {
	scan := NewMemoryScan(testBirds, nil)
	limited := NewLimit(scan, 3)

	result, err := Run(limited)

	assert.NoError(t, err)
	assert.Equal(t, 3, len(result), "should return exactly 3 rows")

	// Verify it's the first 3 tuples
	assert.Equal(t, testBirds[0], result[0])
	assert.Equal(t, testBirds[1], result[1])
	assert.Equal(t, testBirds[2], result[2])
}
