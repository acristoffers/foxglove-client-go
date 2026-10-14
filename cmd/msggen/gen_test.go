package main

import (
	"strings"
	"testing"

	"github.com/acristoffers/foxglove-client-go/ros2msg"
)

func TestNames(t *testing.T) {
	types := map[string]string{
		"geometry_msgs/Point":           "GeometryPoint",
		"example_interfaces/Int32":      "ExampleInt32",
		"builtin_interfaces/Time":       "BuiltinTime",
		"tf2_msgs/TFMessage":            "Tf2TFMessage",
		"type_description_interfaces/X": "TypeDescriptionX",
	}
	for in, want := range types {
		if got := goTypeName(in); got != want {
			t.Errorf("goTypeName(%s) = %s, want %s", in, got, want)
		}
	}

	fields := map[string]string{
		"frame_id":       "FrameID",
		"child_frame_id": "ChildFrameID",
		"x":              "X",
		"iTOW":           "ITOW",
		"cpu_usage":      "CPUUsage",
		"fingerprint":    "Fingerprint_",
		"a__b":           "AB",
	}
	for in, want := range fields {
		if got := goFieldName(in); got != want {
			t.Errorf("goFieldName(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestConstants(t *testing.T) {
	valid := map[ros2msg.Constant]string{
		{Type: ros2msg.Type{Name: "bool"}, Value: "True"}:       "true",
		{Type: ros2msg.Type{Name: "string"}, Value: `"a # b"`}:  `"a # b"`,
		{Type: ros2msg.Type{Name: "string"}, Value: `'quoted'`}: `"quoted"`,
		{Type: ros2msg.Type{Name: "string"}, Value: `bare`}:     `"bare"`,
		{Type: ros2msg.Type{Name: "float64"}, Value: "1e-7"}:    "1e-7",
		{Type: ros2msg.Type{Name: "uint8"}, Value: "0x10"}:      "0x10",
	}
	for c, want := range valid {
		if got, err := goConstant(c); err != nil || got != want {
			t.Errorf("goConstant(%s %s) = %s, %v; want %s", c.Type.Name, c.Value, got, err, want)
		}
	}
	for _, c := range []ros2msg.Constant{
		{Type: ros2msg.Type{Name: "bool"}, Value: "maybe"},
		{Type: ros2msg.Type{Name: "int32"}, Value: "three"},
	} {
		if _, err := goConstant(c); err == nil {
			t.Errorf("goConstant(%s %s) succeeded", c.Type.Name, c.Value)
		}
	}
}

func generate(t *testing.T, name, text string) (string, error) {
	t.Helper()
	schema, err := ros2msg.Parse(name, text)
	if err != nil {
		t.Fatal(err)
	}
	g := &generator{types: map[string]*genType{}}
	if err := g.add(schema); err != nil {
		return "", err
	}
	src, err := g.generate("msgs")
	return string(src), err
}

func TestGenerate(t *testing.T) {
	src, err := generate(t, "pkg/msg/Sample", `
uint8 MODE_A=1
Empty nothing
uint8[] blob
uint8[4] fixed_blob
Item[] items
Item[2] pair
float64[<=3] bounded
==========
MSG: pkg/Item
string name
==========
MSG: pkg/Empty
`)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"PkgSample_MODE_A uint8 = 1",
		"m.Nothing.decode(r)",
		"m.Blob = r.Bytes(r.SequenceLength(1))",
		"copy(m.FixedBlob[:], r.Bytes(4))",
		"m.Items = make([]PkgItem, r.SequenceLength(4))",
		"for i := range m.Pair {",
		"m.Bounded = make([]float64, r.SequenceLength(8))",
		`func (*PkgSample) SchemaName() string { return "pkg/msg/Sample" }`,
		// Empty messages carry a placeholder byte.
		"func (m *PkgEmpty) decode(r *cdr.Reader) {\n\tr.Uint8()\n}",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("generated code lacks %q", want)
		}
	}
}

func TestGenerateRejects(t *testing.T) {
	if _, err := generate(t, "pkg/W", "wstring text"); err == nil {
		t.Error("wstring fields accepted")
	}
	if _, err := generate(t, "pkg/C", "int32 a_b\nint32 aB"); err == nil {
		t.Error("colliding field names accepted")
	}

	g := &generator{types: map[string]*genType{}}
	a, _ := ros2msg.Parse("pkg/A", "Shared s\n=====\nMSG: pkg/Shared\nint32 x")
	b, _ := ros2msg.Parse("pkg/B", "Shared s\n=====\nMSG: pkg/Shared\nfloat64 x")
	g.add(a)
	if err := g.add(b); err == nil {
		t.Error("conflicting definitions of a shared type accepted")
	}
}
