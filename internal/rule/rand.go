package rule

import (
	"crypto/rand"
	"encoding/binary"
)

// Intn returns a uniform value in [0, n). n must be positive.
func Intn(n int) int {
	if n <= 0 {
		panic("rule.Intn: n must be positive")
	}
	if n == 1 {
		return 0
	}
	// Rejection sampling over 63 bits avoids modulo bias.
	un := uint64(n)
	max := uint64(1) << 63
	limit := max - max%un
	for {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			panic(err)
		}
		v := binary.BigEndian.Uint64(b[:]) >> 1
		if v < limit {
			return int(v % un)
		}
	}
}

// Shuffle is Fisher-Yates using Intn.
func Shuffle(n int, swap func(i, j int)) {
	for i := n - 1; i > 0; i-- {
		j := Intn(i + 1)
		swap(i, j)
	}
}
