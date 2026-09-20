package storage

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFSMCreate(t *testing.T) {
	filename := "test_fsm_create.fsm"
	defer os.Remove(filename)

	fsm, err := CreateFSM(filename)
	assert.NoError(t, err)
	defer fsm.Close()

	assert.Equal(t, uint32(0), fsm.PageCount())
}

func TestFSMAddPage(t *testing.T) {
	filename := "test_fsm_addpage.fsm"
	defer os.Remove(filename)

	fsm, err := CreateFSM(filename)
	assert.NoError(t, err)
	defer fsm.Close()

	fsm.AddPage(4000)
	fsm.AddPage(3000)
	fsm.AddPage(2000)

	assert.Equal(t, uint32(3), fsm.PageCount())
	assert.Equal(t, uint16(4000), fsm.Get(0))
	assert.Equal(t, uint16(3000), fsm.Get(1))
	assert.Equal(t, uint16(2000), fsm.Get(2))
}

func TestFSMUpdate(t *testing.T) {
	filename := "test_fsm_update.fsm"
	defer os.Remove(filename)

	fsm, err := CreateFSM(filename)
	assert.NoError(t, err)
	defer fsm.Close()

	fsm.AddPage(4000)
	fsm.Update(0, 3500)

	assert.Equal(t, uint16(3500), fsm.Get(0))
}

func TestFSMFindPageWithSpace(t *testing.T) {
	filename := "test_fsm_find.fsm"
	defer os.Remove(filename)

	fsm, err := CreateFSM(filename)
	assert.NoError(t, err)
	defer fsm.Close()

	fsm.AddPage(100)
	fsm.AddPage(500)
	fsm.AddPage(200)

	// find page with 300 bytes
	pageId, ok := fsm.FindPageWithSpace(300)
	assert.True(t, ok)
	assert.Equal(t, uint32(1), pageId)

	// find page with 600 bytes (none have enough)
	_, ok = fsm.FindPageWithSpace(600)
	assert.False(t, ok)
}

func TestFSMPersistence(t *testing.T) {
	filename := "test_fsm_persist.fsm"
	defer os.Remove(filename)

	// create and save
	fsm1, err := CreateFSM(filename)
	assert.NoError(t, err)

	fsm1.AddPage(1000)
	fsm1.AddPage(2000)
	fsm1.AddPage(3000)
	err = fsm1.save()
	assert.NoError(t, err)
	fsm1.Close()

	// open and verify
	fsm2, err := OpenFSM(filename)
	assert.NoError(t, err)
	defer fsm2.Close()

	assert.Equal(t, uint32(3), fsm2.PageCount())
	assert.Equal(t, uint16(1000), fsm2.Get(0))
	assert.Equal(t, uint16(2000), fsm2.Get(1))
	assert.Equal(t, uint16(3000), fsm2.Get(2))
}

func TestFSMIntegration(t *testing.T) {
	filename := "test_fsm_integration.data"
	defer os.Remove(filename)
	defer os.Remove(filename + ".fsm")

	// insert records
	for i := uint32(1); i <= 5; i++ {
		record := MovieRecord{i, "Movie", "Action"}
		err := InsertRecord(filename, record, 0)
		assert.NoError(t, err)
	}

	// read back
	movies, err := ReadMoviesPages(filename)
	assert.NoError(t, err)
	assert.Equal(t, 5, len(movies))

	// verify FSM file exists
	_, err = os.Stat(filename + ".fsm")
	assert.NoError(t, err)
}
