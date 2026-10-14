# foxglove-client-go

A Go client for the
[Foxglove WebSocket protocol](https://docs.foxglove.dev/docs/sdk/websocket-server), the protocol
spoken by `foxglove_bridge` and any server built on the Foxglove SDK. It also includes packages for
decoding the CDR-encoded ROS 2 messages such servers stream, with or without generating Go types
ahead of time.

```go
client, err := foxglove.Dial(ctx, "ws://localhost:8765", nil)
if err != nil {
	// ...
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
		// ev.Channel describes the payload in ev.Data (e.g. CDR + ros2msg).
	}
}
```

## Packages

| Package                | Purpose                                                                                                                             |
| ---------------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| [`foxglove`](foxglove) | The WebSocket client: connect, subscribe, publish, call services, read/write parameters, fetch assets, follow the connection graph. |
| [`ros2msg`](ros2msg)   | Parses `ros2msg` schemas and decodes CDR payloads generically, without generating any code.                                         |
| [`cdr`](cdr)           | A low-level reader for the CDR wire format underlying ROS 2 messages.                                                               |

Two command-line tools built on the library ship in [`cmd`](cmd):

| Tool                                   | Purpose                                                                                                                                |
| -------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------- |
| [`foxglove-probe`](cmd/foxglove-probe) | Connects to a server, prints what it advertises, and streams topics while reporting their rates — useful to sanity-check a connection. |
| [`msggen`](cmd/msggen)                 | Fetches `.msg` schemas from a running server and generates typed, allocation-light Go decoders from them.                              |

## Install

```sh
go get github.com/acristoffers/foxglove-client-go/foxglove
```

Requires Go 1.26.

## Documentation

- [doc/getting-started.md](doc/getting-started.md) — install, connect, drain events. Start here.
- [doc/client.md](doc/client.md) — the full `Client` API: channels, subscriptions, publishing,
  services, parameters, assets, connection graph.
- [doc/decoding.md](doc/decoding.md) — decoding CDR/`ros2msg` payloads, with or without generated
  types.
- [doc/codegen.md](doc/codegen.md) — generating typed decoders with `msggen`.
- [doc/examples.md](doc/examples.md) — complete runnable programs: a topic logger, a publisher, a
  service client, a parameter editor.

Every exported identifier also carries a doc comment reachable with `go doc` (e.g.
`go doc ./foxglove Client.Subscribe`).

## License

[BSD 3-Clause](LICENSE).
