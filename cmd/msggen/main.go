// Command msggen generates Go types and CDR decoders for ROS 2 messages.
//
// Message definitions are kept as files in a schema directory, one per type,
// at <dir>/<package>/msg/<Type>.msg. Each file holds the definition in the
// "ros2msg" schema encoding, dependencies included, as advertised by
// foxglove_bridge.
//
//	msggen fetch -url ws://localhost:8765 -dir schemas /odom sensor_msgs/msg/Imu
//	msggen fetch -url ws://localhost:8765 -dir schemas   # refresh existing files
//	msggen gen -dir schemas -pkg msgs -out messages_gen.go
//
// fetch downloads definitions from a running bridge, by topic or by type. gen
// writes a Go file with a struct per message, decoders for the CDR encoding,
// and a fingerprint of each layout so that programs can detect when the
// running system's definitions no longer match the generated code.
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}

	var err error
	switch os.Args[1] {
	case "fetch":
		err = fetchCommand(os.Args[2:])
	case "gen":
		err = genCommand(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "msggen:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  msggen fetch -url URL -dir DIR [TOPIC|TYPE...]
  msggen gen -dir DIR -pkg NAME -out FILE`)
	os.Exit(2)
}
