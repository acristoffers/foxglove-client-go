// Package cdr decodes data in the OMG Common Data Representation, the wire
// format of ROS 2 messages.
//
// It supports the plain encodings used for ROS 2 types: XCDR1 (CDR_BE,
// CDR_LE), which is what the default RMW implementations produce, and plain
// XCDR2 (CDR2_BE, CDR2_LE). Mutable and appendable encodings are not
// supported, as ROS 2 message types are always final.
package cdr

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Encapsulation identifies the encoding of a serialized payload, as given by
// its first two bytes.
type Encapsulation uint16

// Encapsulations supported by Reader.
const (
	CDRBigEndian     Encapsulation = 0x0000
	CDRLittleEndian  Encapsulation = 0x0001
	CDR2BigEndian    Encapsulation = 0x0006
	CDR2LittleEndian Encapsulation = 0x0007
)

// ErrShortBuffer is reported when the data ends before the value being read.
var ErrShortBuffer = errors.New("cdr: unexpected end of data")

// Reader reads values from a serialized payload in order.
//
// Errors are sticky: after the first failure every read returns a zero value,
// and Err reports what went wrong. Decoders can read a whole message and
// check Err once at the end.
type Reader struct {
	data     []byte // payload after the encapsulation header
	pos      int
	order    binary.ByteOrder
	maxAlign int
	err      error
}

// NewReader returns a Reader for a payload that starts with its 4-byte
// encapsulation header.
func NewReader(data []byte) (*Reader, error) {
	if len(data) < 4 {
		return nil, fmt.Errorf("cdr: payload of %d bytes has no encapsulation header", len(data))
	}

	r := &Reader{data: data[4:]}
	switch Encapsulation(binary.BigEndian.Uint16(data[0:2])) {
	case CDRBigEndian:
		r.order, r.maxAlign = binary.BigEndian, 8
	case CDRLittleEndian:
		r.order, r.maxAlign = binary.LittleEndian, 8
	case CDR2BigEndian:
		r.order, r.maxAlign = binary.BigEndian, 4
	case CDR2LittleEndian:
		r.order, r.maxAlign = binary.LittleEndian, 4
	default:
		return nil, fmt.Errorf("cdr: unsupported encapsulation %#04x", data[0:2])
	}
	return r, nil
}

// Err returns the first error encountered while reading.
func (r *Reader) Err() error {
	return r.err
}

// Remaining returns the number of unread bytes.
func (r *Reader) Remaining() int {
	return len(r.data) - r.pos
}

// align skips the padding before a value of the given size. Positions are
// relative to the end of the encapsulation header.
func (r *Reader) align(size int) {
	size = min(size, r.maxAlign)
	if pad := r.pos % size; pad != 0 {
		r.pos += size - pad
	}
}

// next aligns for and consumes n bytes, returning nil on error.
func (r *Reader) next(n, alignment int) []byte {
	if r.err != nil {
		return nil
	}
	r.align(alignment)
	if n < 0 || r.pos+n > len(r.data) {
		r.err = ErrShortBuffer
		return nil
	}
	b := r.data[r.pos : r.pos+n]
	r.pos += n
	return b
}

// Bool reads a boolean.
func (r *Reader) Bool() bool {
	return r.Uint8() != 0
}

// Uint8 reads an unsigned 8-bit integer, which is also how ROS byte and char
// fields are encoded.
func (r *Reader) Uint8() uint8 {
	if b := r.next(1, 1); b != nil {
		return b[0]
	}
	return 0
}

// Int8 reads a signed 8-bit integer.
func (r *Reader) Int8() int8 {
	return int8(r.Uint8())
}

// Uint16 reads an unsigned 16-bit integer.
func (r *Reader) Uint16() uint16 {
	if b := r.next(2, 2); b != nil {
		return r.order.Uint16(b)
	}
	return 0
}

// Int16 reads a signed 16-bit integer.
func (r *Reader) Int16() int16 {
	return int16(r.Uint16())
}

// Uint32 reads an unsigned 32-bit integer.
func (r *Reader) Uint32() uint32 {
	if b := r.next(4, 4); b != nil {
		return r.order.Uint32(b)
	}
	return 0
}

// Int32 reads a signed 32-bit integer.
func (r *Reader) Int32() int32 {
	return int32(r.Uint32())
}

// Uint64 reads an unsigned 64-bit integer.
func (r *Reader) Uint64() uint64 {
	if b := r.next(8, 8); b != nil {
		return r.order.Uint64(b)
	}
	return 0
}

// Int64 reads a signed 64-bit integer.
func (r *Reader) Int64() int64 {
	return int64(r.Uint64())
}

// Float32 reads a 32-bit float.
func (r *Reader) Float32() float32 {
	return math.Float32frombits(r.Uint32())
}

// Float64 reads a 64-bit float.
func (r *Reader) Float64() float64 {
	return math.Float64frombits(r.Uint64())
}

// String reads a string: a 32-bit length that counts the terminating NUL,
// followed by the bytes.
func (r *Reader) String() string {
	n := int(r.Uint32())
	b := r.next(n, 1)
	if len(b) > 0 && b[len(b)-1] == 0 {
		b = b[:len(b)-1]
	}
	return string(b)
}

// SequenceLength reads the element count that precedes a sequence. Counts
// that cannot possibly fit in the remaining data are reported as errors, so
// corrupt input never causes huge allocations. minElemSize is the smallest
// number of bytes one element can occupy (at least 1).
func (r *Reader) SequenceLength(minElemSize int) int {
	n := int(r.Uint32())
	if r.err != nil {
		return 0
	}
	if n < 0 || n > r.Remaining()/max(minElemSize, 1) {
		r.err = fmt.Errorf("cdr: sequence of %d elements exceeds the %d remaining bytes", n, r.Remaining())
		return 0
	}
	return n
}

// Bytes reads n raw bytes, as used for uint8 and byte arrays. The returned
// slice is a copy.
func (r *Reader) Bytes(n int) []byte {
	b := r.next(n, 1)
	if b == nil {
		return nil
	}
	return append([]byte(nil), b...)
}
