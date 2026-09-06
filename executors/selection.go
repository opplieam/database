package executors

import (
	"database/storage"
)

// Selection filters child tuples using a predicate function.
// Named "selection" to match relational algebra literature, not SQL SELECT.
//
// Example: Filter non-US birds (isUS = false)
//
// Initial state:
//   s.child = MemoryScan([{"robin", true}, {"ostrich", false}, {"eagle", true}])
//   s.predicate = func(t) { return !t[3].(bool) }  // not isUS
//
// Call 1: Next()
//   t = child.Next() = {"robin", true}
//   predicate(t) = !true = false → skip
//   loop back
//
// Call 2: Next()
//   t = child.Next() = {"ostrich", false}
//   predicate(t) = !false = true → return
//   return {"ostrich", false}
//
// Call 3: Next()
//   t = child.Next() = {"eagle", true}
//   predicate(t) = !true = false → skip
//   loop back
//
// Call 4: Next()
//   t = child.Next() → io.EOF
//   return nil, io.EOF
//
// Note: Selection doesn't buffer. It filters on-the-fly, one tuple at a time.
type Selection struct {
	child     Node
	predicate func(storage.Tuple) bool
}

// NewSelection creates a new Selection executor node.
func NewSelection(child Node, predicate func(storage.Tuple) bool) *Selection {
	return &Selection{child: child, predicate: predicate}
}

// Next returns the next tuple that satisfies the predicate, or io.EOF when exhausted.
func (s *Selection) Next() (storage.Tuple, error) {
	for {
		t, err := s.child.Next()
		if err != nil {
			return nil, err
		}
		if s.predicate(t) {
			return t, nil
		}
	}
}
