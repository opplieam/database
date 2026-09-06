package storage

import (
	"encoding/binary"
	"errors"
)

// Tuple represents a single row as a slice of values.
type Tuple = []any

// EncodeRecord encodes a movie record as bytes:
//
//	[4 bytes] uint32 movieId (little-endian)
//	[1 byte]  uint8 title length
//	[N bytes] title bytes
//	[1 byte]  uint8 genres length
//	[N bytes] genres bytes
func EncodeRecord(movieId uint32, title, genres string) []byte {
	if len(title) > 255 {
		title = title[:255]
	}
	if len(genres) > 255 {
		genres = genres[:255]
	}

	data := make([]byte, 4+1+len(title)+1+len(genres))
	offset := 0

	binary.LittleEndian.PutUint32(data[offset:], movieId)
	offset += 4

	data[offset] = byte(len(title))
	offset++
	copy(data[offset:], title)
	offset += len(title)

	data[offset] = byte(len(genres))
	offset++
	copy(data[offset:], genres)

	return data
}

// DecodeRecord decodes a movie record from bytes.
func DecodeRecord(data []byte) (movieId uint32, title, genres string, err error) {
	if len(data) < 4 {
		return 0, "", "", errors.New("data too short for movieId")
	}

	offset := 0
	movieId = binary.LittleEndian.Uint32(data[offset:])
	offset += 4

	if len(data) < offset+1 {
		return 0, "", "", errors.New("data too short for title length")
	}
	titleLen := int(data[offset])
	offset++

	if len(data) < offset+titleLen {
		return 0, "", "", errors.New("data too short for title")
	}
	title = string(data[offset : offset+titleLen])
	offset += titleLen

	if len(data) < offset+1 {
		return 0, "", "", errors.New("data too short for genres length")
	}
	genresLen := int(data[offset])
	offset++

	if len(data) < offset+genresLen {
		return 0, "", "", errors.New("data too short for genres")
	}
	genres = string(data[offset : offset+genresLen])

	return movieId, title, genres, nil
}
