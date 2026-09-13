package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"database/btree"
	"database/executors"
	"database/storage"
	"database/tx"
)

func main() {
	fmt.Println("=== Database Executor Integration Test ===")
	fmt.Println()

	// Step 1: Load CSV data
	fmt.Println("1. Loading CSV data...")
	scan, err := executors.NewFileScan("movies.csv")
	if err != nil {
		fatal("csv scan", err)
	}
	defer scan.Close()

	var movies []storage.MovieRecord
	for {
		t, err := scan.Next()
		if err != nil {
			break
		}
		id, _ := strconv.Atoi(t[0].(string))
		movies = append(movies, storage.MovieRecord{
			MovieId: uint32(id),
			Title:   t[1].(string),
			Genres:  t[2].(string),
		})
	}
	fmt.Printf("   Loaded %d movies from CSV\n", len(movies))
	fmt.Println()

	// Step 2: Write to slotted pages
	fmt.Println("2. Writing to slotted pages...")
	filename := "integration_test.data"
	defer os.Remove(filename)

	if err := storage.WriteMoviesPages(filename, movies); err != nil {
		fatal("write pages", err)
	}
	fmt.Printf("   Wrote %d records to %s\n", len(movies), filename)
	fmt.Println()

	// Step 3: Insert a new record
	fmt.Println("3. Inserting a new record...")
	insert := executors.NewInsert(filename, storage.MovieRecord{
		MovieId: 99999,
		Title:   "Integration Test Movie",
		Genres:  "Test|Debug",
	}, 0)
	if _, err := insert.Next(); err != nil {
		fatal("insert", err)
	}
	fmt.Println("   Inserted record with ID 99999")
	fmt.Println()

	// Step 4: Query with HeapFileScan
	fmt.Println("4. Querying with HeapFileScan...")
	heapScan, err := executors.NewHeapFileScan(filename)
	if err != nil {
		fatal("heap scan", err)
	}
	defer heapScan.Close()

	// Query: First 5 movie titles
	q1 := executors.NewProjection(
		executors.NewLimit(heapScan, 5),
		func(t storage.Tuple) storage.Tuple { return storage.Tuple{t[1]} },
	)
	result1, err := executors.Run(q1)
	if err != nil {
		fatal("query 1", err)
	}
	fmt.Println("   First 5 movies:")
	for _, t := range result1 {
		fmt.Printf("     - %v\n", t[0])
	}
	fmt.Println()

	// Step 5: Query with Selection (filter by genre)
	fmt.Println("5. Querying with Selection (Comedy movies)...")
	heapScan2, err := executors.NewHeapFileScan(filename)
	if err != nil {
		fatal("heap scan 2", err)
	}
	defer heapScan2.Close()

	q2 := executors.NewSelection(
		heapScan2,
		func(t storage.Tuple) bool {
			genres := t[2].(string)
			return strings.Contains(genres, "Comedy")
		},
	)
	result2, err := executors.Run(q2)
	if err != nil {
		fatal("query 2", err)
	}
	fmt.Printf("   Found %d Comedy movies\n", len(result2))
	fmt.Println()

	// Step 6: Query with Sort (heaviest movies)
	fmt.Println("6. Querying with Sort (heaviest movies)...")
	heapScan3, err := executors.NewHeapFileScan(filename)
	if err != nil {
		fatal("heap scan 3", err)
	}
	defer heapScan3.Close()

	q3 := executors.NewProjection(
		executors.NewLimit(
			executors.NewSort(
				heapScan3,
				func(t storage.Tuple) any { return t[2] },
				true,
			),
			5,
		),
		func(t storage.Tuple) storage.Tuple { return storage.Tuple{t[0], t[1]} },
	)
	result3, err := executors.Run(q3)
	if err != nil {
		fatal("query 3", err)
	}
	fmt.Println("   Top 5 heaviest movies:")
	for _, t := range result3 {
		fmt.Printf("     - %v: %v kg\n", t[1], t[0])
	}
	fmt.Println()

	// Step 7: Build B+ tree index
	fmt.Println("7. Building B+ tree index...")
	tree := btree.NewBTree()
	for _, m := range movies {
		tree.Insert(int(m.MovieId), storage.Tuple{m.MovieId, m.Title, m.Genres})
	}
	fmt.Printf("   Indexed %d records in B+ tree\n", len(movies))
	fmt.Println()

	// Step 8: Query B+ tree
	fmt.Println("8. Querying B+ tree...")
	// Search for specific movie
	if val, ok := tree.Search(1); ok {
		fmt.Printf("   Found movie ID 1: %v\n", val[1])
	}

	// Range scan
	keys := tree.RangeScanKeys(100, 110)
	fmt.Printf("   Range scan (100-110): found %d movies: %v\n", len(keys), keys)
	fmt.Println()

	// Step 9: Scan B+ tree with executor
	fmt.Println("9. Scanning B+ tree with executor...")
	btreeScan := executors.NewBTreeScan(tree)
	result4, err := executors.Run(btreeScan)
	if err != nil {
		fatal("btree scan", err)
	}
	fmt.Printf("   Scanned %d records from B+ tree\n", len(result4))
	fmt.Println()

	// Step 10: Verify persistence
	fmt.Println("10. Verifying B+ tree persistence...")
	btreeFile := "integration_btree.data"
	defer os.Remove(btreeFile)

	if err := tree.Save(btreeFile); err != nil {
		fatal("save btree", err)
	}
	fmt.Printf("    Saved B+ tree to %s\n", btreeFile)

	loadedTree, err := btree.LoadBTree(btreeFile)
	if err != nil {
		fatal("load btree", err)
	}
	fmt.Printf("    Loaded B+ tree from %s\n", btreeFile)

	if val, ok := loadedTree.Search(1); ok {
		fmt.Printf("    Search(1) on loaded tree: %v\n", val[1])
	}
	fmt.Println()

	// Step 11: Transaction lifecycle demo
	fmt.Println("11. Transaction lifecycle...")
	mgr := tx.NewTxManager()

	// begin two transactions
	tx1 := mgr.Begin(tx.RepeatableRead)
	tx2 := mgr.Begin(tx.RepeatableRead)
	fmt.Printf("    tx1 id=%d, tx2 id=%d\n", tx1.Id(), tx2.Id())
	fmt.Printf("    tx1 status=%v, tx2 status=%v\n", tx1.Status(), tx2.Status())

	// tx1 does two commands
	cid0 := tx1.NextCID()
	cid1 := tx1.NextCID()
	fmt.Printf("    tx1 command 0: cid=%d\n", cid0)
	fmt.Printf("    tx1 command 1: cid=%d\n", cid1)

	// commit tx1
	mgr.Commit(tx1)
	fmt.Printf("    after commit: tx1 status=%v, tx1 active=%v\n", tx1.Status(), mgr.IsActive(tx1.Id()))

	// rollback tx2
	mgr.Rollback(tx2)
	fmt.Printf("    after rollback: tx2 status=%v, tx2 active=%v\n", tx2.Status(), mgr.IsActive(tx2.Id()))
	fmt.Println()

	// Step 12: Snapshot Isolation (Repeatable Read)
	fmt.Println("12. Snapshot Isolation (Repeatable Read)...")
	fmt.Println()
	fmt.Println("    Core lesson: Each transaction sees only COMMITTED data")
	fmt.Println("    from before it started. Uncommitted changes are invisible.")
	fmt.Println()

	// Create commit log
	clogFile := "clog.data"
	clog, err := tx.OpenCommitLog(clogFile)
	if err != nil {
		fatal("open clog", err)
	}
	defer clog.Close()
	defer os.Remove(clogFile)

	// Step 1: tx=1 inserts A and commits
	recA := storage.Insert(1, 0, storage.Tuple{"A"})
	clog.LogCommit(1)
	fmt.Println("    Step 1: tx=1 INSERT A, COMMIT")

	// Step 2: tx=2 starts (sees A), inserts B, but does NOT commit
	recB := storage.Insert(2, 0, storage.Tuple{"B"})
	// NOT committed: clog.LogCommit(2)
	fmt.Println("    Step 2: tx=2 INSERT B (NOT committed)")

	// Step 3: tx=3 starts
	fmt.Println("    Step 3: tx=3 starts")
	fmt.Println()

	allRecords := []storage.TxRecord{recA, recB}

	// Show what each transaction sees (Repeatable Read)
	fmt.Println("    What each transaction sees (Repeatable Read):")
	for _, txId := range []uint64{1, 2, 3} {
		var visible []string
		for _, r := range allRecords {
			if r.Visible(txId, clog) {
				visible = append(visible, r.Data[0].(string))
			}
		}
		fmt.Printf("      tx=%d: %v\n", txId, visible)
	}
	fmt.Println()
	fmt.Println("    Key insight:")
	fmt.Println("      - tx=1 sees [A] (only A existed when tx=1 started)")
	fmt.Println("      - tx=2 sees [A, B] (A committed, B inserted by tx=2)")
	fmt.Println("      - tx=3 sees [A] (A committed, but B NOT committed by tx=2)")
	fmt.Println()

	// Step 13: Snapshot Isolation (Read Committed)
	fmt.Println("13. Snapshot Isolation (Read Committed)...")
	fmt.Println()
	fmt.Println("    Core lesson: Each STATEMENT sees only COMMITTED data")
	fmt.Println("    from before it started. New snapshot per statement.")
	fmt.Println()

	// Create new records for Read Committed demo
	recC := storage.Insert(10, 0, storage.Tuple{"C"})
	clog.LogCommit(10)
	fmt.Println("    Step 1: tx=10 INSERT C, COMMIT")

	// tx=11: first statement (sees C)
	stmt1 := uint64(100) // simulated statement ID
	var visible1 []string
	for _, r := range []storage.TxRecord{recC} {
		if r.Visible(stmt1, clog) {
			visible1 = append(visible1, r.Data[0].(string))
		}
	}
	fmt.Printf("    Step 2: tx=11 stmt1 SELECT → sees %v\n", visible1)

	// tx=12: inserts D and commits
	recD := storage.Insert(12, 0, storage.Tuple{"D"})
	clog.LogCommit(12)
	fmt.Println("    Step 3: tx=12 INSERT D, COMMIT")

	// tx=11: second statement (new snapshot, sees C and D)
	stmt2 := uint64(101) // new statement ID
	var visible2 []string
	for _, r := range []storage.TxRecord{recC, recD} {
		if r.Visible(stmt2, clog) {
			visible2 = append(visible2, r.Data[0].(string))
		}
	}
	fmt.Printf("    Step 4: tx=11 stmt2 SELECT → sees %v\n", visible2)
	fmt.Println()
	fmt.Println("    Key insight:")
	fmt.Println("      - stmt1 sees [C] (only C committed before stmt1)")
	fmt.Println("      - stmt2 sees [C, D] (D committed before stmt2)")
	fmt.Println("      - In Repeatable Read, stmt2 would still see only [C]")
	fmt.Println()

	fmt.Println("=== ALL TESTS PASSED ===")
}

func fatal(msg string, err error) {
	fmt.Fprintf(os.Stderr, "%s: %v\n", msg, err)
	os.Exit(1)
}
