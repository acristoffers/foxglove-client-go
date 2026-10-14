// Package ros2msg parses ROS 2 message definitions in the "ros2msg" schema
// encoding: the text of a .msg file, followed by the definitions of every type
// it depends on, each introduced by a separator line of "=" and a
// "MSG: package/Type" header. This is the format foxglove_bridge advertises
// and MCAP files store.
package ros2msg

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
)

// ArrayKind tells whether and how a field is an array.
type ArrayKind int

const (
	// NotArray is a single value.
	NotArray ArrayKind = iota
	// FixedArray is T[N]: exactly Length elements, serialized without a count.
	FixedArray
	// UnboundedArray is T[]: a sequence of any length.
	UnboundedArray
	// BoundedArray is T[<=N]: a sequence of at most Length elements.
	BoundedArray
)

// Type is the type of a field or constant.
type Type struct {
	// Name is a primitive type such as "float64" or "string", or the
	// fully qualified name of a message, such as "geometry_msgs/Point".
	Name string
	// StringBound is N for string<=N and wstring<=N, zero otherwise.
	StringBound int
	Array       ArrayKind
	// Length is the size of a FixedArray, or the bound of a BoundedArray.
	Length int
}

// IsPrimitive reports whether the type is a built-in type rather than a
// message.
func (t Type) IsPrimitive() bool {
	_, ok := primitiveSizes[t.Name]
	return ok
}

// String formats the type as it appears in a .msg file.
func (t Type) String() string {
	s := t.Name
	if t.StringBound > 0 {
		s += "<=" + strconv.Itoa(t.StringBound)
	}
	switch t.Array {
	case FixedArray:
		s += "[" + strconv.Itoa(t.Length) + "]"
	case UnboundedArray:
		s += "[]"
	case BoundedArray:
		s += "[<=" + strconv.Itoa(t.Length) + "]"
	}
	return s
}

// primitiveSizes maps the built-in types to their serialized size in bytes.
// Strings are variable length; their size is that of the length prefix.
var primitiveSizes = map[string]int{
	"bool":    1,
	"byte":    1,
	"char":    1,
	"int8":    1,
	"uint8":   1,
	"int16":   2,
	"uint16":  2,
	"int32":   4,
	"uint32":  4,
	"int64":   8,
	"uint64":  8,
	"float32": 4,
	"float64": 8,
	"string":  4,
	"wstring": 4,
}

// PrimitiveSize returns the serialized size of a primitive type, or 0 if the
// name is not a primitive. Strings report the size of their length prefix.
func PrimitiveSize(name string) int {
	return primitiveSizes[name]
}

// Field is a field of a message.
type Field struct {
	Name string
	Type Type
	// Default is the default value as written in the definition, if any.
	Default string
}

// Constant is a constant declared in a message.
type Constant struct {
	Name string
	Type Type
	// Value is the value as written in the definition.
	Value string
}

// Definition is a single message type.
type Definition struct {
	// Name is the fully qualified name, e.g. "geometry_msgs/Point".
	Name      string
	Fields    []Field
	Constants []Constant
}

// Package returns the package the message belongs to.
func (d *Definition) Package() string {
	pkg, _, _ := strings.Cut(d.Name, "/")
	return pkg
}

// Schema is a message definition together with all the types it uses.
type Schema struct {
	Root *Definition
	// Definitions holds every type of the schema, including Root, by fully
	// qualified name.
	Definitions map[string]*Definition
}

// Normalize turns a type name into its fully qualified form by dropping the
// "msg" infix: "geometry_msgs/msg/Point" becomes "geometry_msgs/Point".
func Normalize(name string) string {
	if pkg, rest, ok := strings.Cut(name, "/msg/"); ok {
		return pkg + "/" + rest
	}
	return name
}

// Parse parses a schema. name is the type of the root message, in either
// "pkg/Type" or "pkg/msg/Type" form.
func Parse(name, text string) (*Schema, error) {
	name = Normalize(name)
	if !strings.Contains(name, "/") {
		return nil, fmt.Errorf("ros2msg: schema name %q is not of the form package/Type", name)
	}

	s := &Schema{Definitions: map[string]*Definition{}}

	sectionName := name
	var section []string
	flush := func() error {
		def, err := parseDefinition(sectionName, section)
		if err != nil {
			return err
		}
		if _, dup := s.Definitions[def.Name]; dup {
			return fmt.Errorf("ros2msg: %s is defined twice", def.Name)
		}
		s.Definitions[def.Name] = def
		if s.Root == nil {
			s.Root = def
		}
		return nil
	}

	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for i := 0; i < len(lines); i++ {
		if !isSeparator(lines[i]) {
			section = append(section, lines[i])
			continue
		}
		if err := flush(); err != nil {
			return nil, err
		}
		// The separator is followed by a "MSG: pkg/Type" header.
		i++
		for i < len(lines) && strings.TrimSpace(lines[i]) == "" {
			i++
		}
		if i == len(lines) {
			return nil, fmt.Errorf("ros2msg: separator without a MSG header at the end of %s", name)
		}
		header, ok := strings.CutPrefix(strings.TrimSpace(lines[i]), "MSG:")
		if !ok {
			return nil, fmt.Errorf("ros2msg: expected a MSG header after a separator, got %q", lines[i])
		}
		sectionName = Normalize(strings.TrimSpace(header))
		section = nil
	}
	if err := flush(); err != nil {
		return nil, err
	}

	if err := s.resolve(); err != nil {
		return nil, err
	}
	return s, nil
}

func isSeparator(line string) bool {
	line = strings.TrimSpace(line)
	return len(line) >= 3 && strings.Trim(line, "=") == ""
}

func parseDefinition(name string, lines []string) (*Definition, error) {
	def := &Definition{Name: name}
	for i, line := range lines {
		line = strings.TrimSpace(stripComment(line))
		if line == "" {
			continue
		}
		if err := def.parseLine(line); err != nil {
			return nil, fmt.Errorf("ros2msg: %s line %d: %w", name, i+1, err)
		}
	}
	return def, nil
}

// stripComment removes a trailing # comment, leaving # characters inside
// quoted constant values alone.
func stripComment(line string) string {
	var quote rune
	for i, c := range line {
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '#':
			return line[:i]
		}
	}
	return line
}

func (d *Definition) parseLine(line string) error {
	typeToken, rest, ok := cutSpace(line)
	if !ok {
		return fmt.Errorf("expected a type and a name in %q", line)
	}

	typ, err := parseType(typeToken)
	if err != nil {
		return err
	}

	// Constants are the only declarations with an "=" after the type.
	if name, value, ok := strings.Cut(rest, "="); ok {
		name = strings.TrimSpace(name)
		if !validName(name) {
			return fmt.Errorf("invalid constant name %q", name)
		}
		if !typ.IsPrimitive() || typ.Array != NotArray {
			return fmt.Errorf("constant %s must have a primitive, non-array type", name)
		}
		d.Constants = append(d.Constants, Constant{Name: name, Type: typ, Value: strings.TrimSpace(value)})
		return nil
	}

	name, def, _ := cutSpace(rest)
	if !validName(name) {
		return fmt.Errorf("invalid field name %q", name)
	}
	d.Fields = append(d.Fields, Field{Name: name, Type: typ, Default: strings.TrimSpace(def)})
	return nil
}

// cutSpace splits s around its first run of whitespace.
func cutSpace(s string) (before, after string, found bool) {
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		return s, "", false
	}
	return s[:i], strings.TrimSpace(s[i:]), true
}

func validName(name string) bool {
	if name == "" || !isLetter(name[0]) {
		return false
	}
	for i := 1; i < len(name); i++ {
		if !isLetter(name[i]) && !isDigit(name[i]) && name[i] != '_' {
			return false
		}
	}
	return true
}

func isLetter(c byte) bool { return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' }
func isDigit(c byte) bool  { return '0' <= c && c <= '9' }

func parseType(token string) (Type, error) {
	var t Type
	base := token

	if open := strings.IndexByte(token, '['); open >= 0 {
		if !strings.HasSuffix(token, "]") {
			return t, fmt.Errorf("malformed array type %q", token)
		}
		base = token[:open]
		inner := token[open+1 : len(token)-1]
		switch {
		case inner == "":
			t.Array = UnboundedArray
		case strings.HasPrefix(inner, "<="):
			n, ok := parseLength(inner[2:])
			if !ok {
				return t, fmt.Errorf("invalid array bound in %q", token)
			}
			t.Array, t.Length = BoundedArray, n
		default:
			n, ok := parseLength(inner)
			if !ok {
				return t, fmt.Errorf("invalid array length in %q", token)
			}
			t.Array, t.Length = FixedArray, n
		}
	}

	if name, bound, ok := strings.Cut(base, "<="); ok {
		if name != "string" && name != "wstring" {
			return t, fmt.Errorf("only strings can be bounded, got %q", token)
		}
		n, ok := parseLength(bound)
		if !ok {
			return t, fmt.Errorf("invalid string bound in %q", token)
		}
		base, t.StringBound = name, n
	}

	if base == "" {
		return t, fmt.Errorf("missing type in %q", token)
	}
	t.Name = base
	return t, nil
}

// maxLength bounds array lengths and bounds and string bounds. CDR counts
// are 32 bits, and keeping within int32 also keeps lengths representable as
// int on every platform.
const maxLength = math.MaxInt32

func parseLength(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil && n > 0 && n <= maxLength
}

// resolve qualifies every field type and checks that all of them are defined
// and that no type contains itself.
func (s *Schema) resolve() error {
	for _, def := range s.Definitions {
		for i := range def.Fields {
			t := &def.Fields[i].Type
			if t.IsPrimitive() {
				continue
			}
			t.Name = s.qualify(def.Package(), t.Name)
			if _, ok := s.Definitions[t.Name]; !ok {
				return fmt.Errorf("ros2msg: %s.%s has type %s, which the schema does not define", def.Name, def.Fields[i].Name, t.Name)
			}
		}
	}
	return s.checkCycles(s.Root.Name, nil)
}

func (s *Schema) qualify(pkg, name string) string {
	if strings.Contains(name, "/") {
		return Normalize(name)
	}
	if local := pkg + "/" + name; s.Definitions[local] != nil || name != "Header" {
		return local
	}
	// ROS 1 era definitions use a bare Header for std_msgs/Header.
	return "std_msgs/Header"
}

func (s *Schema) checkCycles(name string, path []string) error {
	if slices.Contains(path, name) {
		return fmt.Errorf("ros2msg: %s contains itself (%s)", name, strings.Join(append(path, name), " -> "))
	}
	for _, f := range s.Definitions[name].Fields {
		if !f.Type.IsPrimitive() {
			if err := s.checkCycles(f.Type.Name, append(path, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

// Dependencies returns the fully qualified names of the types the definition
// uses, directly or not, sorted by name.
func (s *Schema) Dependencies(name string) []string {
	seen := map[string]bool{}
	var visit func(string)
	visit = func(name string) {
		for _, f := range s.Definitions[name].Fields {
			if !f.Type.IsPrimitive() && !seen[f.Type.Name] {
				seen[f.Type.Name] = true
				visit(f.Type.Name)
			}
		}
	}
	visit(name)
	deps := make([]string, 0, len(seen))
	for dep := range seen {
		deps = append(deps, dep)
	}
	slices.Sort(deps)
	return deps
}

// Fingerprint identifies the wire layout of a type: its fields' names and
// types, and those of every type it uses. Comments, whitespace, default
// values and constants do not affect it. Two definitions with the same
// fingerprint serialize identically.
func (s *Schema) Fingerprint(name string) string {
	name = Normalize(name)
	var b strings.Builder
	for _, n := range append([]string{name}, s.Dependencies(name)...) {
		b.WriteString(n)
		b.WriteByte('\n')
		for _, f := range s.Definitions[n].Fields {
			fmt.Fprintf(&b, "%s %s\n", f.Type, f.Name)
		}
	}
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

// ConstantName returns the name of the constant with the given prefix and
// value, without the prefix: with "uint8 STATE_RISING = 2", ConstantName(
// "STATE_", uint8(2)) is "RISING". Messages name the values of their enum-like
// fields this way.
func (d *Definition) ConstantName(prefix string, value any) (string, bool) {
	want := fmt.Sprint(value)
	for _, c := range d.Constants {
		if strings.HasPrefix(c.Name, prefix) && strings.TrimSpace(c.Value) == want {
			return strings.TrimPrefix(c.Name, prefix), true
		}
	}
	return "", false
}
