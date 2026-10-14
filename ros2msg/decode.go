package ros2msg

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/acristoffers/foxglove-client-go/cdr"
)

// Message is a message decoded without a generated type, its fields in the
// order of the definition.
type Message struct {
	// Type is the fully qualified name of the message, e.g. "geometry_msgs/Point".
	Type   string
	Fields []FieldValue
}

// FieldValue is a decoded field. Values are bool, int8 to uint64, float32,
// float64, string, *Message, or []any of those for arrays.
type FieldValue struct {
	Name  string
	Value any
}

// Decode decodes a CDR payload, encapsulation header included, as a message
// of the schema's root type.
func (s *Schema) Decode(data []byte) (*Message, error) {
	r, err := cdr.NewReader(data)
	if err != nil {
		return nil, err
	}
	msg, err := s.decodeMessage(r, s.Root.Name)
	if err != nil {
		return nil, err
	}
	if err := r.Err(); err != nil {
		return nil, err
	}
	return msg, nil
}

func (s *Schema) decodeMessage(r *cdr.Reader, name string) (*Message, error) {
	def := s.Definitions[name]
	msg := &Message{Type: name, Fields: make([]FieldValue, 0, len(def.Fields))}
	if len(def.Fields) == 0 {
		// Empty messages are serialized with a one byte placeholder.
		r.Uint8()
	}
	for _, f := range def.Fields {
		value, err := s.decodeField(r, f.Type)
		if err != nil {
			return nil, fmt.Errorf("%s.%s: %w", name, f.Name, err)
		}
		msg.Fields = append(msg.Fields, FieldValue{Name: f.Name, Value: value})
	}
	return msg, nil
}

func (s *Schema) decodeField(r *cdr.Reader, t Type) (any, error) {
	if t.Array == NotArray {
		return s.decodeValue(r, t)
	}
	minSize := max(PrimitiveSize(t.Name), 1)
	n := t.Length
	if t.Array != FixedArray {
		n = r.SequenceLength(minSize)
	} else if n > r.Remaining()/minSize {
		// Fixed arrays carry no count to check, so the length from the
		// schema is checked against the payload before allocating.
		return nil, fmt.Errorf("array of %d elements exceeds the %d remaining bytes", n, r.Remaining())
	}
	elem := t
	elem.Array, elem.Length = NotArray, 0
	values := make([]any, n)
	for i := range values {
		value, err := s.decodeValue(r, elem)
		if err != nil {
			return nil, err
		}
		values[i] = value
	}
	return values, nil
}

func (s *Schema) decodeValue(r *cdr.Reader, t Type) (any, error) {
	switch t.Name {
	case "bool":
		return r.Bool(), nil
	case "byte", "char", "uint8":
		return r.Uint8(), nil
	case "int8":
		return r.Int8(), nil
	case "int16":
		return r.Int16(), nil
	case "uint16":
		return r.Uint16(), nil
	case "int32":
		return r.Int32(), nil
	case "uint32":
		return r.Uint32(), nil
	case "int64":
		return r.Int64(), nil
	case "uint64":
		return r.Uint64(), nil
	case "float32":
		return r.Float32(), nil
	case "float64":
		return r.Float64(), nil
	case "string":
		return r.String(), nil
	case "wstring":
		return nil, fmt.Errorf("wstring is not supported")
	default:
		return s.decodeMessage(r, t.Name)
	}
}

// YAML formats the message the way rosidl's to_yaml does in block style, as
// ROS nodes print messages in their logs.
func (m *Message) YAML() string {
	var b strings.Builder
	writeMessage(&b, m, 0)
	return b.String()
}

func writeMessage(b *strings.Builder, m *Message, indent int) {
	pad := strings.Repeat(" ", indent)
	for _, f := range m.Fields {
		switch value := f.Value.(type) {
		case *Message:
			fmt.Fprintf(b, "%s%s:\n", pad, f.Name)
			writeMessage(b, value, indent+2)
		case []any:
			if len(value) == 0 {
				fmt.Fprintf(b, "%s%s: []\n", pad, f.Name)
				continue
			}
			fmt.Fprintf(b, "%s%s:\n", pad, f.Name)
			for _, item := range value {
				if s, ok := item.(*Message); ok {
					fmt.Fprintf(b, "%s-\n", pad)
					writeMessage(b, s, indent+2)
				} else {
					fmt.Fprintf(b, "%s- %s\n", pad, scalarYAML(item))
				}
			}
		default:
			fmt.Fprintf(b, "%s%s: %s\n", pad, f.Name, scalarYAML(value))
		}
	}
}

// scalarYAML formats a value like rosidl: floats keep their decimal point
// with 6 significant digits (C++'s std::showpoint), strings are quoted.
func scalarYAML(value any) string {
	switch v := value.(type) {
	case bool:
		return strconv.FormatBool(v)
	case float32:
		return floatYAML(float64(v))
	case float64:
		return floatYAML(v)
	case string:
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
	default:
		return fmt.Sprint(v)
	}
}

func floatYAML(v float64) string {
	switch {
	case math.IsNaN(v):
		return "nan"
	case math.IsInf(v, 1):
		return "inf"
	case math.IsInf(v, -1):
		return "-inf"
	}
	return fmt.Sprintf("%#g", v)
}

// Get returns the value at a path of field names, e.g. Get("pose",
// "position", "x").
func (m *Message) Get(path ...string) (any, bool) {
	var value any = m
	for _, name := range path {
		msg, ok := value.(*Message)
		if !ok {
			return nil, false
		}
		found := false
		for _, f := range msg.Fields {
			if f.Name == name {
				value, found = f.Value, true
				break
			}
		}
		if !found {
			return nil, false
		}
	}
	return value, true
}
