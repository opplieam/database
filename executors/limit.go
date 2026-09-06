package executors

import (
	"io"

	"database/storage"
)

// Limit returns at most n tuples from its child, then stops.
//
// Example: Limit 2
//
// Initial state:
//   l.child = MemoryScan([{"A"}, {"B"}, {"C"}])
//   l.n = 2
//   l.count = 0
//
// Call 1: Next()
//   count=0 < n=2 → true
//   t = child.Next() = {"A"}
//   count becomes 1
//   return {"A"}
//
// Call 2: Next()
//   count=1 < n=2 → true
//   t = child.Next() = {"B"}
//   count becomes 2
//   return {"B"}
//
// Call 3: Next()
//   count=2 < n=2 → false
//   return nil, io.EOF
//
// Note: Even though child has "C", we stop at n=2.
type Limit struct {
	child Node
	n     int
	count int
}

// NewLimit creates a new Limit executor node.
func NewLimit(child Node, n int) *Limit {
	return &Limit{child: child, n: n}
}

// Next returns the next tuple from the child (up to n times), or io.EOF when limit reached.
func (l *Limit) Next() (storage.Tuple, error) {
	if l.count >= l.n {
		return nil, io.EOF
	}
	t, err := l.child.Next()
	if err != nil {
		return nil, err
	}
	l.count++
	return t, nil
}
