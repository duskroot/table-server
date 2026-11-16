package id

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// New returns prefix plus 16 hex chars from crypto/rand.
func New(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return prefix + hex.EncodeToString(b[:])
}

// RequestID is a random UUID version 4 string.
func RequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// RoomCode is a 6-digit numeric code. It never returns 000000.
func RoomCode() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	n := int(b[0])<<16 | int(b[1])<<8 | int(b[2])
	n = n%900000 + 100000
	return fmt.Sprintf("%06d", n)
}
