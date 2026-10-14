# Examples

Complete, runnable programs. Each is a full `main.go` — `go run` it after
`go get github.com/acristoffers/foxglove-client-go/foxglove`. All of them default to
`ws://localhost:8765`, the default `foxglove_bridge` address.

See also `cmd/foxglove-probe` and `cmd/msggen` in the repository itself for two tools built on this
library using the same patterns, which you can run directly without copying any code.

## 1. Logging every message on a topic

Connects, waits for a topic to be advertised, subscribes, and prints each message generically
decoded — no code generation, works for any ROS 2 topic.

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"github.com/acristoffers/foxglove-client-go/foxglove"
	"github.com/acristoffers/foxglove-client-go/ros2msg"
)

func main() {
	url := flag.String("url", "ws://localhost:8765", "server URL")
	topic := flag.String("topic", "/odom", "topic to log")
	flag.Parse()

	ctx := context.Background()
	client, err := foxglove.Dial(ctx, *url, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	var schema *ros2msg.Schema
	for ev := range client.Events() {
		switch ev := ev.(type) {
		case foxglove.Advertise:
			for _, ch := range ev.Channels {
				if ch.Topic != *topic || ch.Encoding != "cdr" {
					continue
				}
				text, err := ch.SchemaData()
				if err != nil {
					log.Fatal(err)
				}
				schema, err = ros2msg.Parse(ch.SchemaName, string(text))
				if err != nil {
					log.Fatal(err)
				}
				if _, err := client.Subscribe(ch.ID); err != nil {
					log.Fatal(err)
				}
			}

		case foxglove.MessageData:
			msg, err := schema.Decode(ev.Data)
			if err != nil {
				log.Println("decode:", err)
				continue
			}
			fmt.Println("---")
			fmt.Print(msg.YAML())

		case foxglove.Status:
			log.Printf("[%s] %s", ev.Level, ev.Message)
		}
	}
	log.Fatal(client.Err())
}
```

## 2. Publishing JSON messages

Advertises a channel and publishes to it once a second. Works against any server that lists `"json"`
in `ServerInfo.SupportedEncodings` and advertises `CapabilityClientPublish`.

```go
package main

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/acristoffers/foxglove-client-go/foxglove"
)

func main() {
	ctx := context.Background()
	client, err := foxglove.Dial(ctx, "ws://localhost:8765", nil)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	if !client.ServerInfo().Has(foxglove.CapabilityClientPublish) {
		log.Fatal("server does not support client publishing")
	}

	chID, err := client.Advertise(foxglove.ClientChannel{
		Topic:      "/heartbeat",
		Encoding:   "json",
		SchemaName: "heartbeat",
		// A permissive schema; tighten it if downstream consumers need more.
		SchemaEncoding: "jsonschema",
		Schema:         []byte(`{"type":"object"}`),
	})
	if err != nil {
		log.Fatal(err)
	}
	defer client.Unadvertise(chID)

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for range ticker.C {
		payload, _ := json.Marshal(map[string]int64{"timestamp": time.Now().UnixNano()})
		if err := client.Publish(chID, payload); err != nil {
			log.Println("publish:", err)
		}
	}
}
```

## 3. Calling a service

Calls a service by name and prints the raw response bytes.

```go
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"time"

	"github.com/acristoffers/foxglove-client-go/foxglove"
)

func main() {
	url := flag.String("url", "ws://localhost:8765", "server URL")
	name := flag.String("service", "/reset", "service name")
	flag.Parse()

	ctx := context.Background()
	client, err := foxglove.Dial(ctx, *url, nil)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	// Services may arrive after the initial connection; wait for it.
	var svc *foxglove.Service
	for svc == nil {
		var ok bool
		if svc, ok = client.ServiceByName(*name); ok {
			break
		}
		ev, ok := <-client.Events()
		if !ok {
			log.Fatal(client.Err())
		}
		_ = ev
	}

	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := client.CallService(callCtx, svc.ID, "", nil)

	var svcErr *foxglove.ServiceCallError
	switch {
	case errors.As(err, &svcErr):
		log.Fatalf("service failed: %s", svcErr.Message)
	case err != nil:
		log.Fatal(err)
	default:
		log.Printf("ok, %s reply, %d bytes", resp.Encoding, len(resp.Payload))
	}
}
```

## 4. Reading and writing a parameter

```go
package main

import (
	"context"
	"log"
	"time"

	"github.com/acristoffers/foxglove-client-go/foxglove"
)

func main() {
	ctx := context.Background()
	client, err := foxglove.Dial(ctx, "ws://localhost:8765", nil)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	opCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	before, err := client.GetParameters(opCtx, "robot.max_speed")
	if err != nil {
		log.Fatal(err)
	}
	if v, ok := before[0].AsFloat64(); ok {
		log.Println("current max_speed:", v)
	}

	after, err := client.SetParameters(opCtx, foxglove.Float64Parameter("robot.max_speed", 1.5))
	if err != nil {
		log.Fatal(err)
	}
	if v, ok := after[0].AsFloat64(); ok {
		log.Println("new max_speed:", v)
	}
}
```

## 5. Decoding with generated types

After running `msggen` (see [codegen.md](codegen.md)) against a schema directory, decoding becomes a
direct `UnmarshalCDR` call instead of a schema parse:

```go
package main

import (
	"context"
	"log"

	"github.com/acristoffers/foxglove-client-go/foxglove"
	"example.com/myrobot/msgs" // your msggen-generated package
)

func main() {
	ctx := context.Background()
	client, err := foxglove.Dial(ctx, "ws://localhost:8765", nil)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	for ev := range client.Events() {
		switch ev := ev.(type) {
		case foxglove.Advertise:
			for _, ch := range ev.Channels {
				if ch.Topic == "/odom" {
					client.Subscribe(ch.ID)
				}
			}
		case foxglove.MessageData:
			var odom msgs.NavOdometry
			if err := odom.UnmarshalCDR(ev.Data); err != nil {
				log.Println("decode:", err)
				continue
			}
			log.Println(odom.Pose.Pose.Position.X, odom.Pose.Pose.Position.Y)
		}
	}
}
```
