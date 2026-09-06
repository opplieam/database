package executors

import (
	"io"

	"database/storage"
)

// Tuple is an alias for storage.Tuple for backward compatibility.
type Tuple = storage.Tuple

// Node is the interface every executor node implements.
// Each call to Next returns the next tuple, or io.EOF when done.
type Node interface {
	Next() (Tuple, error)
}

// Run exhausts a node by calling Next repeatedly until io.EOF,
// collecting all tuples into a slice.
func Run(node Node) ([]Tuple, error) {
	var result []Tuple
	for {
		t, err := node.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	return result, nil
}
