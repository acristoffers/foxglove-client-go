# Getting started

## Install

```sh
go get github.com/acristoffers/foxglove-client-go/foxglove
```

The module requires Go 1.26. `cdr` and `ros2msg` are separate packages you `go get`/import only if
you need them (see [Decoding messages](decoding.md)) — most programs that only publish or only
consume pre-decoded JSON channels never import them directly.

## What the client talks to

`foxglove.Client` speaks the
[Foxglove WebSocket protocol](https://docs.foxglove.dev/docs/sdk/websocket-server), the
protocol implemented by `foxglove_bridge` (the ROS/ROS 2 bridge) and by any server built on the
Foxglove SDK. It negotiates whichever of the two known subprotocols the server supports
(`foxglove.sdk.v1`, used by current SDK-based servers, or the legacy `foxglove.websocket.v1`), so
the same client code works against either.

## Connecting

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/acristoffers/foxglove-client-go/foxglove"
)

func main() {
	ctx := context.Background()
	client, err := foxglove.Dial(ctx, "ws://localhost:8765", nil)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	fmt.Println("connected to", client.ServerInfo().Name)
}
```

`ctx` only bounds the handshake — once `Dial` returns, the connection lives until `Close` is called
or the server goes away, independent of the context passed in. Pass a context with a timeout if you
want the connection attempt itself to give up after a while:

```go
dialCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
client, err := foxglove.Dial(dialCtx, "ws://localhost:8765", nil)
```

`Dial`'s third argument, `*foxglove.DialOptions`, is optional (`nil` picks sensible defaults) and
lets you set a custom `http.Client`, add HTTP headers to the handshake, change the `Events`
channel's buffer size, or raise the per-message read limit for servers that stream large point
clouds or images.

## Draining events

Every message the server sends — advertisements, data on subscribed channels, status updates,
parameter changes — arrives on one channel, `Client.Events()`. The single exception is replies to
requests you made yourself (`CallService`, `GetParameters`, `SetParameters`, `FetchAsset`), which
are returned directly by the method that made the request instead of being delivered as events.

```go
for ev := range client.Events() {
	switch ev := ev.(type) {
	case foxglove.ServerInfo:
		fmt.Println("server:", ev.Name, "capabilities:", ev.Capabilities)

	case foxglove.Advertise:
		for _, ch := range ev.Channels {
			if ch.Topic == "/odom" {
				if _, err := client.Subscribe(ch.ID); err != nil {
					log.Println("subscribe:", err)
				}
			}
		}

	case foxglove.MessageData:
		fmt.Printf("%s: %d bytes\n", ev.Channel.Topic, len(ev.Data))

	case foxglove.Status:
		log.Printf("server status [%s]: %s", ev.Level, ev.Message)
	}
}
// The loop above exits once Events() is closed, which happens when the
// connection ends. Client.Err() then reports why.
if err := client.Err(); err != nil {
	log.Println("connection ended:", err)
}
```

This loop matters more than it looks: `Client` reads from the socket only as fast as `Events()` is
drained. A consumer that stops reading (blocked in some other call, or just slow) makes the client
stop reading too, which lets the server's own backpressure handling kick in instead of the client
building an unbounded backlog in memory. Don't spin up a goroutine that reads every event into an
unbounded slice "to deal with later" — read what you need and discard or act on the rest promptly.

The very first event `Dial` ever delivers is the `ServerInfo` the server sent during the handshake
(the same value `Client.ServerInfo()` returns), so a `switch` like the one above sees it even though
`Dial` already consumed it internally to find the server's capabilities.

## Where to next

- [client.md](client.md) covers the rest of `Client`'s methods: publishing, calling services,
  parameters, assets, and the connection graph.
- [decoding.md](decoding.md) covers turning `MessageData.Data` into something you can read, for
  `cdr`-encoded (ROS 2) channels.
- [examples.md](examples.md) has complete programs, including the topic logger shown piece by piece
  above.
