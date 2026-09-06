package executors

import (
	"fmt"
	"io"
	"sort"
	"strconv"

	"database/storage"
)

// Sort consumes all child tuples, sorts them by a key function,
// then returns them one at a time.
//
// Example: Sort birds by weight descending
//
// Initial state:
//   s.child = MemoryScan([{"robin", 0.077}, {"eagle", 4.74}, {"penguin", 23.0}])
//   s.key = func(t) { return t[2] }  // weight
//   s.desc = true
//   s.buffer = []
//   s.idx = 0
//   s.loaded = false
//
// Call 1: Next()
//   loaded=false → consume all child tuples
//   buffer = [{"robin", 0.077}, {"eagle", 4.74}, {"penguin", 23.0}]
//   sort by weight descending: [{"penguin", 23.0}, {"eagle", 4.74}, {"robin", 0.077}]
//   loaded=true
//   idx=0 < len=3 → true
//   return {"penguin", 23.0}, idx becomes 1
//
// Call 2: Next()
//   loaded=true
//   idx=1 < len=3 → true
//   return {"eagle", 4.74}, idx becomes 2
//
// Call 3: Next()
//   loaded=true
//   idx=2 < len=3 → true
//   return {"robin", 0.077}, idx becomes 3
//
// Call 4: Next()
//   loaded=true
//   idx=3 < len=3 → false
//   return nil, io.EOF
type Sort struct {
	child   Node
	key     func(storage.Tuple) any
	desc    bool
	buffer  []storage.Tuple
	idx     int
	loaded  bool
}

// NewSort creates a new Sort executor node.
func NewSort(child Node, key func(storage.Tuple) any, desc bool) *Sort {
	return &Sort{child: child, key: key, desc: desc}
}

// Next returns the next sorted tuple, or io.EOF when exhausted.
func (s *Sort) Next() (storage.Tuple, error) {
	if !s.loaded {
		for {
			t, err := s.child.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			s.buffer = append(s.buffer, t)
		}
		sort.SliceStable(s.buffer, func(i, j int) bool {
			ki, kj := s.key(s.buffer[i]), s.key(s.buffer[j])
			// try numeric comparison first
			fi, ei := strconv.ParseFloat(fmt.Sprint(ki), 64)
			fj, ej := strconv.ParseFloat(fmt.Sprint(kj), 64)
			if ei == nil && ej == nil {
				if s.desc {
					return fi > fj
				}
				return fi < fj
			}
			// fallback to string comparison
			if s.desc {
				return fmt.Sprint(ki) > fmt.Sprint(kj)
			}
			return fmt.Sprint(ki) < fmt.Sprint(kj)
		})
		s.loaded = true
	}

	if s.idx >= len(s.buffer) {
		return nil, io.EOF
	}
	t := s.buffer[s.idx]
	s.idx++
	return t, nil
}
