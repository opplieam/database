package executors

import (
	"io"

	"database/storage"
)

// MemoryScan iterates over an in-memory table (slice of tuples).
// Used for testing before we add disk-based scans.
//
// Example state transitions for a table with 3 rows:
//
// Initial state:
//   m.table = [{"Toy Story", "Adventure"}, {"Jumanji", "Comedy"}, {"Heat", "Action"}]
//   m.idx = 0
//
// Call 1: Next()
//   idx=0 < len=3 → true
//   t = table[0] = {"Toy Story", "Adventure"}
//   idx becomes 1
//   return {"Toy Story", "Adventure"}
//
// Call 2: Next()
//   idx=1 < len=3 → true
//   t = table[1] = {"Jumanji", "Comedy"}
//   idx becomes 2
//   return {"Jumanji", "Comedy"}
//
// Call 3: Next()
//   idx=2 < len=3 → true
//   t = table[2] = {"Heat", "Action"}
//   idx becomes 3
//   return {"Heat", "Action"}
//
// Call 4: Next()
//   idx=3 < len=3 → false
//   return nil, io.EOF
type MemoryScan struct {
	table []storage.Tuple
	idx   int
}

// NewMemoryScan creates a new MemoryScan executor node.
func NewMemoryScan(table []storage.Tuple) *MemoryScan {
	return &MemoryScan{table: table}
}

// Next returns the next tuple from the in-memory table, or io.EOF when exhausted.
func (m *MemoryScan) Next() (storage.Tuple, error) {
	if m.idx >= len(m.table) {
		return nil, io.EOF
	}
	t := m.table[m.idx]
	m.idx++
	return t, nil
}
