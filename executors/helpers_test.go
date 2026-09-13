package executors

import "database/storage"

// testBirds is shared test data for executor tests.
// Fields: [code, name, weight, isUS]
var testBirds = []storage.Tuple{
	{"amerob", "American Robin", 0.077, true},
	{"baleag", "Bald Eagle", 4.74, true},
	{"eursta", "European Starling", 0.082, true},
	{"barswa", "Barn Swallow", 0.019, true},
	{"ostric1", "Ostrich", 104.0, false},
	{"emppen1", "Emperor Penguin", 23.0, false},
	{"rufhum", "Rufous Hummingbird", 0.0034, true},
	{"comrav", "Common Raven", 1.2, true},
	{"wanalb", "Wandering Albatross", 8.5, false},
	{"norcar", "Northern Cardinal", 0.045, true},
}
