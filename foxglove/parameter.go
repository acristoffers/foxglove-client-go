package foxglove

import (
	"encoding/base64"
	"encoding/json"
	"errors"
)

// ParameterType disambiguates parameter values that JSON cannot represent on
// its own.
type ParameterType string

// Parameter types defined by the protocol.
const (
	// ParameterByteArray marks a base64 encoded string holding raw bytes.
	ParameterByteArray ParameterType = "byte_array"
	// ParameterFloat64 marks a number that must be treated as a float even if
	// it has no fractional part.
	ParameterFloat64 ParameterType = "float64"
	// ParameterFloat64Array is the array counterpart of ParameterFloat64.
	ParameterFloat64Array ParameterType = "float64_array"
)

// Parameter is a named parameter, typically a ROS node parameter such as
// "/robot_state_publisher.publish_frequency".
//
// Values received from the server are nil (unset), bool, string, json.Number,
// []any or map[string]any. Use the accessor methods to read them, and the
// constructor functions to build parameters for Client.SetParameters.
type Parameter struct {
	Name  string        `json:"name"`
	Type  ParameterType `json:"type,omitempty"`
	Value any           `json:"value,omitempty"`
}

// BoolParameter returns a boolean parameter.
func BoolParameter(name string, value bool) Parameter {
	return Parameter{Name: name, Value: value}
}

// IntParameter returns an integer parameter.
func IntParameter(name string, value int64) Parameter {
	return Parameter{Name: name, Value: value}
}

// Float64Parameter returns a floating point parameter.
func Float64Parameter(name string, value float64) Parameter {
	return Parameter{Name: name, Type: ParameterFloat64, Value: value}
}

// StringParameter returns a string parameter.
func StringParameter(name string, value string) Parameter {
	return Parameter{Name: name, Value: value}
}

// ByteArrayParameter returns a byte array parameter.
func ByteArrayParameter(name string, value []byte) Parameter {
	return Parameter{Name: name, Type: ParameterByteArray, Value: base64.StdEncoding.EncodeToString(value)}
}

// IntArrayParameter returns an integer array parameter.
func IntArrayParameter(name string, values []int64) Parameter {
	return Parameter{Name: name, Value: values}
}

// Float64ArrayParameter returns a floating point array parameter.
func Float64ArrayParameter(name string, values []float64) Parameter {
	return Parameter{Name: name, Type: ParameterFloat64Array, Value: values}
}

// BoolArrayParameter returns a boolean array parameter.
func BoolArrayParameter(name string, values []bool) Parameter {
	return Parameter{Name: name, Value: values}
}

// StringArrayParameter returns a string array parameter.
func StringArrayParameter(name string, values []string) Parameter {
	return Parameter{Name: name, Value: values}
}

// UnsetParameter returns a parameter without a value, which asks the server
// to remove it when passed to Client.SetParameters.
func UnsetParameter(name string) Parameter {
	return Parameter{Name: name}
}

// IsSet reports whether the parameter has a value.
func (p Parameter) IsSet() bool {
	return p.Value != nil
}

// AsBool returns the value as a bool.
func (p Parameter) AsBool() (bool, bool) {
	v, ok := p.Value.(bool)
	return v, ok
}

// AsString returns the value as a string. Byte arrays are returned base64
// encoded, use AsBytes to decode them.
func (p Parameter) AsString() (string, bool) {
	v, ok := p.Value.(string)
	return v, ok
}

// AsInt returns the value as an integer. It fails for floats, even ones without
// a fractional part.
func (p Parameter) AsInt() (int64, bool) {
	switch v := p.Value.(type) {
	case json.Number:
		i, err := v.Int64()
		return i, err == nil
	case int64:
		return v, true
	default:
		return 0, false
	}
}

// AsFloat64 returns the value as a float. Integers are converted.
func (p Parameter) AsFloat64() (float64, bool) {
	switch v := p.Value.(type) {
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case float64:
		return v, true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}

// AsBytes decodes a byte array parameter.
func (p Parameter) AsBytes() ([]byte, error) {
	s, ok := p.Value.(string)
	if p.Type != ParameterByteArray || !ok {
		return nil, errors.New("parameter is not a byte array")
	}
	return base64.StdEncoding.DecodeString(s)
}
