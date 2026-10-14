package ros2msg

import (
	"strings"
	"testing"
)

const poseStamped = `# A Pose with reference coordinate frame and timestamp
std_msgs/Header header
Pose pose
================================================================================
MSG: geometry_msgs/Pose
Point position
Quaternion orientation

================================================================================
MSG: geometry_msgs/Point
float64 x
float64 y
float64 z

================================================================================
MSG: geometry_msgs/Quaternion
float64 x 0
float64 y 0
float64 z 0
float64 w 1

================================================================================
MSG: std_msgs/Header
builtin_interfaces/Time stamp
string frame_id

================================================================================
MSG: builtin_interfaces/Time
int32 sec
uint32 nanosec
`

func TestParse(t *testing.T) {
	s, err := Parse("geometry_msgs/msg/PoseStamped", poseStamped)
	if err != nil {
		t.Fatal(err)
	}
	if s.Root.Name != "geometry_msgs/PoseStamped" || len(s.Definitions) != 6 {
		t.Fatalf("root %s with %d definitions", s.Root.Name, len(s.Definitions))
	}
	// Unqualified types resolve within the package of the message using them.
	if got := s.Root.Fields[1].Type.Name; got != "geometry_msgs/Pose" {
		t.Errorf("pose type = %s", got)
	}
	if got := s.Definitions["geometry_msgs/Pose"].Fields[0].Type.Name; got != "geometry_msgs/Point" {
		t.Errorf("position type = %s", got)
	}
	if w := s.Definitions["geometry_msgs/Quaternion"].Fields[3]; w.Name != "w" || w.Default != "1" {
		t.Errorf("quaternion w = %+v", w)
	}
	want := []string{"builtin_interfaces/Time", "geometry_msgs/Point", "geometry_msgs/Pose", "geometry_msgs/Quaternion", "std_msgs/Header"}
	if deps := s.Dependencies(s.Root.Name); strings.Join(deps, " ") != strings.Join(want, " ") {
		t.Errorf("dependencies = %v", deps)
	}
}

func TestParseFields(t *testing.T) {
	s, err := Parse("pkg/All", `
uint8 OK=0     # constants
uint8 ERROR = 2
string GREETING="hello # world"
bool[] flags
float32[3] rgb
int64[<=10] bounded
string<=8 short
string<=8[2] shorts
	byte	tabbed
Header header
==========
MSG: std_msgs/Header
string frame_id
`)
	if err != nil {
		t.Fatal(err)
	}
	def := s.Root

	wantConstants := []Constant{
		{Name: "OK", Type: Type{Name: "uint8"}, Value: "0"},
		{Name: "ERROR", Type: Type{Name: "uint8"}, Value: "2"},
		{Name: "GREETING", Type: Type{Name: "string"}, Value: `"hello # world"`},
	}
	if len(def.Constants) != len(wantConstants) {
		t.Fatalf("constants = %+v", def.Constants)
	}
	for i, c := range wantConstants {
		if def.Constants[i] != c {
			t.Errorf("constant %d = %+v, want %+v", i, def.Constants[i], c)
		}
	}

	wantFields := map[string]string{
		"flags":   "bool[]",
		"rgb":     "float32[3]",
		"bounded": "int64[<=10]",
		"short":   "string<=8",
		"shorts":  "string<=8[2]",
		"tabbed":  "byte",
		"header":  "std_msgs/Header",
	}
	if len(def.Fields) != len(wantFields) {
		t.Fatalf("fields = %+v", def.Fields)
	}
	for _, f := range def.Fields {
		if got := f.Type.String(); got != wantFields[f.Name] {
			t.Errorf("%s has type %s, want %s", f.Name, got, wantFields[f.Name])
		}
	}
}

func TestParseErrors(t *testing.T) {
	tests := map[string]string{
		"undefined type":         "Missing thing",
		"bad array":              "int32[x] values",
		"zero length":            "int32[0] values",
		"huge length":            "uint8[9223372036854775807] data",
		"huge bound":             "uint8[<=2147483648] data",
		"huge string bound":      "string<=9223372036854775807 text",
		"bounded non-string":     "int32<=3 value",
		"bad name":               "int32 2value",
		"no name":                "int32",
		"array constant":         "int32[] VALUES=1",
		"separator without MSG":  "int32 a\n=====\n",
		"separator before field": "int32 a\n=====\nint32 b",
		"cycle":                  "Node child\n=====\nMSG: pkg/Node\nNode next",
		"duplicate definition":   "int32 a\n=====\nMSG: pkg/Root\nint32 b",
	}
	for name, text := range tests {
		if _, err := Parse("pkg/Root", text); err == nil {
			t.Errorf("%s: parsed without error", name)
		}
	}
	if _, err := Parse("Unqualified", "int32 a"); err == nil {
		t.Error("unqualified schema name accepted")
	}
}

func TestBareHeader(t *testing.T) {
	s, err := Parse("pkg/Root", "Header header\n=====\nMSG: std_msgs/Header\nstring frame_id")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Root.Fields[0].Type.Name; got != "std_msgs/Header" {
		t.Errorf("Header resolved to %s", got)
	}
}

func TestFingerprint(t *testing.T) {
	fingerprint := func(text string) string {
		t.Helper()
		s, err := Parse("geometry_msgs/PoseStamped", text)
		if err != nil {
			t.Fatal(err)
		}
		return s.Fingerprint(s.Root.Name)
	}

	base := fingerprint(poseStamped)
	if len(base) != 16 {
		t.Errorf("fingerprint %q is not 16 hex digits", base)
	}

	cosmetic := strings.ReplaceAll(poseStamped, "float64 w 1", "float64  w   # no default anymore")
	cosmetic = "# leading comment\n\n" + strings.ReplaceAll(cosmetic, "Pose pose", "geometry_msgs/msg/Pose pose")
	if got := fingerprint(cosmetic); got != base {
		t.Error("comments, spacing, defaults or name qualification changed the fingerprint")
	}

	for name, text := range map[string]string{
		"field type": strings.Replace(poseStamped, "uint32 nanosec", "int32 nanosec", 1),
		"field name": strings.Replace(poseStamped, "string frame_id", "string frame", 1),
		"new field":  strings.Replace(poseStamped, "float64 z\n", "float64 z\nfloat64 t\n", 1),
	} {
		if fingerprint(text) == base {
			t.Errorf("changing the %s kept the fingerprint", name)
		}
	}

	// Fingerprints of nested types depend only on what they contain.
	s, _ := Parse("geometry_msgs/PoseStamped", poseStamped)
	point, _ := Parse("geometry_msgs/Point", "float64 x\nfloat64 y\nfloat64 z")
	if s.Fingerprint("geometry_msgs/msg/Point") != point.Fingerprint("geometry_msgs/Point") {
		t.Error("the same type has different fingerprints in different schemas")
	}
}
