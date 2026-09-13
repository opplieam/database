package executors

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMemoryScan(t *testing.T) {
	scan := NewMemoryScan(testBirds)
	result, err := Run(scan)

	assert.NoError(t, err)
	assert.Equal(t, len(testBirds), len(result))

	// Verify first and last tuples
	assert.Equal(t, testBirds[0], result[0])
	assert.Equal(t, testBirds[len(testBirds)-1], result[len(result)-1])
}
