package vector

import (
	"encoding/binary"
	"fmt"
	"math"
)

// Serialize converts a float32 slice into a little-endian byte slice suitable
// for storage inside SQLite BLOB columns.
func Serialize(vec []float32) []byte {
	out := make([]byte, 4*len(vec))
	for i, v := range vec {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(v))
	}
	return out
}

// Deserialize converts a byte slice produced by Serialize back to a float32
// slice.
func Deserialize(data []byte) ([]float32, error) {
	if len(data)%4 != 0 {
		return nil, fmt.Errorf("invalid vector blob length %d", len(data))
	}
	n := len(data) / 4
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		bits := binary.LittleEndian.Uint32(data[i*4:])
		out[i] = math.Float32frombits(bits)
	}
	return out, nil
}
