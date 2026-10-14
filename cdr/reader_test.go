package cdr

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

func TestAlignmentCDR1(t *testing.T) {
	// uint8, then a float64 aligned to 8, then a uint16 aligned to 2.
	data := []byte{0x00, 0x01, 0x00, 0x00, 7, 0, 0, 0, 0, 0, 0, 0}
	data = binary.LittleEndian.AppendUint64(data, math.Float64bits(1.5))
	data = append(data, 0xff) // uint8
	data = append(data, 0)    // padding
	data = binary.LittleEndian.AppendUint16(data, 513)

	r, err := NewReader(data)
	if err != nil {
		t.Fatal(err)
	}
	if v := r.Uint8(); v != 7 {
		t.Errorf("uint8 = %d", v)
	}
	if v := r.Float64(); v != 1.5 {
		t.Errorf("float64 = %v", v)
	}
	if v := r.Uint8(); v != 0xff {
		t.Errorf("uint8 = %d", v)
	}
	if v := r.Uint16(); v != 513 {
		t.Errorf("uint16 = %d", v)
	}
	if r.Err() != nil || r.Remaining() != 0 {
		t.Errorf("err = %v, remaining = %d", r.Err(), r.Remaining())
	}
}

func TestAlignmentCDR2(t *testing.T) {
	// XCDR2 aligns 8 byte values to 4 only.
	data := []byte{0x00, 0x07, 0x00, 0x00, 7, 0, 0, 0}
	data = binary.LittleEndian.AppendUint64(data, math.Float64bits(-2))

	r, err := NewReader(data)
	if err != nil {
		t.Fatal(err)
	}
	r.Uint8()
	if v := r.Float64(); v != -2 || r.Err() != nil {
		t.Errorf("float64 = %v, err = %v", v, r.Err())
	}
}

func TestBigEndian(t *testing.T) {
	data := []byte{0x00, 0x00, 0x00, 0x00}
	data = binary.BigEndian.AppendUint32(data, 0xdeadbeef)
	data = binary.BigEndian.AppendUint16(data, 0xfffe)

	r, err := NewReader(data)
	if err != nil {
		t.Fatal(err)
	}
	if v := r.Uint32(); v != 0xdeadbeef {
		t.Errorf("uint32 = %#x", v)
	}
	if v := r.Int16(); v != -2 {
		t.Errorf("int16 = %d", v)
	}
}

func TestStringsAndSequences(t *testing.T) {
	data := []byte{0x00, 0x01, 0x00, 0x00}
	data = binary.LittleEndian.AppendUint32(data, 6)
	data = append(data, "hello\x00"...)
	data = append(data, 0, 0) // padding
	data = binary.LittleEndian.AppendUint32(data, 1)
	data = append(data, 0) // empty string
	data = append(data, 0, 0, 0)
	data = binary.LittleEndian.AppendUint32(data, 0) // empty string without NUL
	data = binary.LittleEndian.AppendUint32(data, 3)
	data = append(data, 1, 2, 3)

	r, err := NewReader(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"hello", "", ""} {
		if s := r.String(); s != want {
			t.Errorf("string = %q, want %q", s, want)
		}
	}
	if b := r.Bytes(r.SequenceLength(1)); string(b) != "\x01\x02\x03" {
		t.Errorf("bytes = %v", b)
	}
	if r.Err() != nil {
		t.Error(r.Err())
	}
}

func TestErrorsAreSticky(t *testing.T) {
	r, err := NewReader([]byte{0x00, 0x01, 0x00, 0x00, 1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if v := r.Uint32(); v != 0 || !errors.Is(r.Err(), ErrShortBuffer) {
		t.Errorf("short uint32 = %d, err = %v", v, r.Err())
	}
	// Even reads that would fit fail once an error occurred.
	if v := r.Uint8(); v != 0 {
		t.Errorf("uint8 after error = %d", v)
	}
}

func TestSequenceLengthGuard(t *testing.T) {
	data := binary.LittleEndian.AppendUint32([]byte{0x00, 0x01, 0x00, 0x00}, 1_000_000_000)
	r, _ := NewReader(append(data, 1, 2, 3, 4))
	if n := r.SequenceLength(8); n != 0 || r.Err() == nil {
		t.Errorf("impossible sequence length accepted: %d, %v", n, r.Err())
	}
}

func TestUnsupportedEncapsulation(t *testing.T) {
	for _, header := range [][]byte{{}, {0x00, 0x01}, {0x00, 0x03, 0, 0}, {0x00, 0x09, 0, 0}} {
		if _, err := NewReader(header); err == nil {
			t.Errorf("NewReader(% x) succeeded", header)
		}
	}
}
