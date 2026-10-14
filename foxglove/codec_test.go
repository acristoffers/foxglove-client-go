package foxglove

import (
	"encoding/binary"
	"testing"
)

func TestParseServerBinaryShort(t *testing.T) {
	tests := map[string][]byte{
		"empty":                        {},
		"message data":                 {opMessageData, 1, 0, 0, 0, 0, 0, 0},
		"time":                         {opTime, 1, 2, 3},
		"service call response":        {opServiceCallResponse, 1, 0, 0, 0, 2, 0, 0, 0},
		"service call response length": binary.LittleEndian.AppendUint32([]byte{opServiceCallResponse, 1, 0, 0, 0, 2, 0, 0, 0}, 10),
		"fetch asset response":         {opFetchAssetResponse, 1, 0, 0, 0, 0},
		"fetch asset error length":     binary.LittleEndian.AppendUint32([]byte{opFetchAssetResponse, 1, 0, 0, 0, 1}, 10),
		"fetch asset invalid status":   binary.LittleEndian.AppendUint32([]byte{opFetchAssetResponse, 1, 0, 0, 0, 7}, 0),
	}
	for name, data := range tests {
		if ev, err := parseServerBinary(data); err == nil {
			t.Errorf("%s: parsed %#v, want error", name, ev)
		}
	}
}

func TestParseServerBinaryUnknownOpcode(t *testing.T) {
	ev, err := parseServerBinary([]byte{0x05, 1, 2, 3})
	if ev != nil || err != nil {
		t.Errorf("unknown opcode = %#v, %v; want it ignored", ev, err)
	}
}

func TestParseServerJSON(t *testing.T) {
	if ev, err := parseServerJSON([]byte(`{"op":"playbackState"}`)); ev != nil || err != nil {
		t.Errorf("unknown op = %#v, %v; want it ignored", ev, err)
	}
	if _, err := parseServerJSON([]byte(`not json`)); err == nil {
		t.Error("invalid JSON parsed")
	}

	ev, err := parseServerJSON([]byte(`{"op":"serverInfo","name":"bridge","capabilities":["services"],
		"dataStartTime":{"sec":1,"nsec":5},"metadata":{"ROS_DISTRO":"jazzy"}}`))
	if err != nil {
		t.Fatal(err)
	}
	info := ev.(ServerInfo)
	if info.Metadata["ROS_DISTRO"] != "jazzy" || info.DataStartTime.Nanoseconds() != 1_000_000_005 {
		t.Errorf("server info = %+v", info)
	}
}

func TestSchemaData(t *testing.T) {
	text := Channel{Encoding: "cdr", SchemaEncoding: "ros2msg", Schema: "bool data"}
	if data, err := text.SchemaData(); err != nil || string(data) != "bool data" {
		t.Errorf("ros2msg schema = %q, %v", data, err)
	}

	binarySchema := Channel{Encoding: "protobuf", SchemaEncoding: "protobuf", Schema: "3q0="}
	if data, err := binarySchema.SchemaData(); err != nil || string(data) != "\xde\xad" {
		t.Errorf("protobuf schema = %x, %v", data, err)
	}

	// Without a schema encoding, the message encoding decides.
	implicit := Channel{Encoding: "protobuf", Schema: "3q0="}
	if data, err := implicit.SchemaData(); err != nil || string(data) != "\xde\xad" {
		t.Errorf("implicit protobuf schema = %x, %v", data, err)
	}
}
