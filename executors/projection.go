package executors

import (
	"database/storage"
)

// Projection maps each child tuple through a function, returning a new tuple.
// For example, picking a subset of columns.
//
// Example: Pick only name and weight from bird records
//
// Initial state:
//   p.child = MemoryScan([{"robin", "American Robin", 0.077, true}, ...])
//   p.proj = func(t) { return Tuple{t[0], t[2]} }  // pick id, weight
//
// Call 1: Next()
//   t = child.Next() = {"robin", "American Robin", 0.077, true}
//   proj(t) = {"robin", 0.077}
//   return {"robin", 0.077}
//
// Call 2: Next()
//   t = child.Next() = {"eagle", "Bald Eagle", 4.74, true}
//   proj(t) = {"eagle", 4.74}
//   return {"eagle", 4.74}
//
// The projection function runs once per tuple. It can:
//   - Pick columns: func(t) { return Tuple{t[0], t[1]} }
//   - Rename columns: func(t) { return Tuple{t[1], "weight=" + t[2]} }
//   - Compute new values: func(t) { return Tuple{t[0], t[2].(float64) * 1000} }
type Projection struct {
	child Node
	proj  func(storage.Tuple) storage.Tuple
}

// NewProjection creates a new Projection executor node.
func NewProjection(child Node, proj func(storage.Tuple) storage.Tuple) *Projection {
	return &Projection{child: child, proj: proj}
}

// Next returns the next projected tuple, or io.EOF when exhausted.
func (p *Projection) Next() (storage.Tuple, error) {
	t, err := p.child.Next()
	if err != nil {
		return nil, err
	}
	return p.proj(t), nil
}
