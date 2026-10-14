package ros2msg

import (
	"encoding/binary"
	"math"
	"testing"
)

// encoder writes little endian CDR, aligned as the cdr package reads it.
type encoder struct{ buf []byte }

func newEncoder() *encoder { return &encoder{buf: []byte{0, 1, 0, 0}} }

func (e *encoder) align(n int) {
	for (len(e.buf)-4)%n != 0 {
		e.buf = append(e.buf, 0)
	}
}
func (e *encoder) u8(v uint8)   { e.buf = append(e.buf, v) }
func (e *encoder) u32(v uint32) { e.align(4); e.buf = binary.LittleEndian.AppendUint32(e.buf, v) }
func (e *encoder) f64(v float64) {
	e.align(8)
	e.buf = binary.LittleEndian.AppendUint64(e.buf, math.Float64bits(v))
}
func (e *encoder) str(s string) { e.u32(uint32(len(s) + 1)); e.buf = append(append(e.buf, s...), 0) }

const toolSchema = `Dimensions dimensions
string name
bool active
float64[2] limits
uint8[] codes
Connection[] connections
Empty nothing
================================================================================
MSG: pkg/Dimensions
float64 length
float64 width
================================================================================
MSG: pkg/Connection
string serial_number
string tool_controller
================================================================================
MSG: pkg/Empty
`

func TestDecodeYAML(t *testing.T) {
	s, err := Parse("pkg/msg/Tool", toolSchema)
	if err != nil {
		t.Fatal(err)
	}

	e := newEncoder()
	e.f64(1)
	e.f64(540)
	e.str(`mower "A"`)
	e.u8(1)
	e.f64(0)
	e.f64(math.Inf(1))
	e.u32(2)
	e.u8(7)
	e.u8(9)
	e.u32(2)
	e.str("LIFT")
	e.str("lift")
	e.str("PTO")
	e.str("pto")
	e.u8(0) // the empty message's placeholder

	msg, err := s.Decode(e.buf)
	if err != nil {
		t.Fatal(err)
	}
	want := `dimensions:
  length: 1.00000
  width: 540.000
name: "mower \"A\""
active: true
limits:
- 0.00000
- inf
codes:
- 7
- 9
connections:
-
  serial_number: "LIFT"
  tool_controller: "lift"
-
  serial_number: "PTO"
  tool_controller: "pto"
nothing:
`
	if got := msg.YAML(); got != want {
		t.Errorf("YAML:\n%s\nwant:\n%s", got, want)
	}
	if msg.Type != "pkg/Tool" || msg.Fields[0].Value.(*Message).Type != "pkg/Dimensions" {
		t.Errorf("types = %s, %s", msg.Type, msg.Fields[0].Value.(*Message).Type)
	}

	empty, _ := Parse("pkg/List", "int32[] values")
	e = newEncoder()
	e.u32(0)
	msg, err = empty.Decode(e.buf)
	if err != nil || msg.YAML() != "values: []\n" {
		t.Errorf("empty sequence = %q, %v", msg.YAML(), err)
	}
}

func TestDecodeErrors(t *testing.T) {
	s, _ := Parse("pkg/msg/Tool", toolSchema)
	e := newEncoder()
	e.f64(1)
	if _, err := s.Decode(e.buf); err == nil {
		t.Error("truncated message decoded")
	}
	// Fixed arrays have no count on the wire; a length the payload cannot
	// hold must fail instead of allocating.
	big, err := Parse("pkg/Big", "float64[2147483647] data")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := big.Decode(newEncoder().buf); err == nil {
		t.Error("fixed array longer than the payload decoded")
	}
	w, _ := Parse("pkg/W", "wstring text")
	if _, err := w.Decode(newEncoder().buf); err == nil {
		t.Error("wstring decoded")
	}
}

func TestGetAndConstantName(t *testing.T) {
	s, err := Parse("pkg/msg/State", `
uint8 STATE_STOP = 1
uint8 STATE_RISING = 2
uint8 MODE_AUTO=2
uint8 state
Inner diagnostics
================================================================================
MSG: pkg/Inner
uint8 level
`)
	if err != nil {
		t.Fatal(err)
	}
	e := newEncoder()
	e.u8(2)
	e.u8(5)
	msg, err := s.Decode(e.buf)
	if err != nil {
		t.Fatal(err)
	}

	if v, ok := msg.Get("diagnostics", "level"); !ok || v != uint8(5) {
		t.Errorf("diagnostics.level = %v, %v", v, ok)
	}
	if _, ok := msg.Get("diagnostics", "missing"); ok {
		t.Error("missing field found")
	}
	if _, ok := msg.Get("state", "deeper"); ok {
		t.Error("path through a scalar found")
	}

	state, _ := msg.Get("state")
	if name, ok := s.Root.ConstantName("STATE_", state); !ok || name != "RISING" {
		t.Errorf("state name = %q, %v", name, ok)
	}
	if name, ok := s.Root.ConstantName("MODE_", state); !ok || name != "AUTO" {
		t.Errorf("mode name = %q, %v", name, ok)
	}
	if _, ok := s.Root.ConstantName("STATE_", uint8(9)); ok {
		t.Error("unknown value named")
	}
}
