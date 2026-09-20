package storage

// NewTupleReader creates a TupleReader function for a given heap file.
//
// Usage:
//
//	readTuple := storage.NewTupleReader("movies.data")
//	tuple, err := readTuple(TID{PageId: 0, SlotId: 1})
func NewTupleReader(path string) func(TID) (Tuple, error) {
	return func(tid TID) (Tuple, error) {
		// 1. Open file
		file, err := Open(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()

		// 2. Read page
		page, err := file.ReadPage(tid.PageId)
		if err != nil {
			return nil, err
		}

		// 3. Get record bytes
		recordBytes, err := page.GetRecord(int(tid.SlotId))
		if err != nil {
			return nil, err
		}

		// 4. Decode record to Tuple
		movieId, title, genres, err := DecodeRecord(recordBytes)
		if err != nil {
			return nil, err
		}

		// 5. Return as Tuple
		return Tuple{movieId, title, genres}, nil
	}
}
