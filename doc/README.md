# foxglove-client-go documentation

This directory documents the three packages that make up the library, and the two command-line tools
built on top of them.

- [Getting started](getting-started.md) — installing the module, dialing a server, and draining the
  event stream. Start here.
- [Client API](client.md) — the full `foxglove.Client` surface: channels, subscriptions, publishing,
  services, parameters, assets and the connection graph, plus the concurrency rules that make it
  safe to use from several goroutines.
- [Decoding messages](decoding.md) — the `cdr` and `ros2msg` packages, for turning channel payloads
  into Go values without hand-writing a decoder for every message type.
- [Generating typed decoders](codegen.md) — the `msggen` tool, which turns a directory of `.msg`
  schemas into a Go file with one struct and decoder per message type.
- [Examples](examples.md) — complete, runnable programs: a topic logger, a publisher, a service
  client, and a parameter editor.

For the one-paragraph version and a copy-pasteable quick start, see the
[top-level README](../README.md).

## Package overview

| Package    | Import path                                           | Purpose                                                                                                                                                                |
| ---------- | ----------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `foxglove` | `github.com/acristoffers/foxglove-client-go/foxglove` | The WebSocket client: connect, subscribe, publish, call services, read/write parameters, fetch assets.                                                                 |
| `ros2msg`  | `github.com/acristoffers/foxglove-client-go/ros2msg`  | Parses `ros2msg`-encoded schemas and decodes CDR payloads into generic `*ros2msg.Message` values, with no code generation required.                                    |
| `cdr`      | `github.com/acristoffers/foxglove-client-go/cdr`      | A low-level reader for the CDR wire format (the serialization ROS 2 uses). `ros2msg` and `msggen`-generated code are built on it; most users only touch it indirectly. |

## Command-line tools

| Tool             | Import path                                                     | Purpose                                                                                                                                                                      |
| ---------------- | --------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `foxglove-probe` | `github.com/acristoffers/foxglove-client-go/cmd/foxglove-probe` | Connects to a server, prints what it advertises, and optionally streams topics while reporting their rates. Useful to sanity-check a connection or a server's configuration. |
| `msggen`         | `github.com/acristoffers/foxglove-client-go/cmd/msggen`         | `fetch`es `.msg` schemas from a running server and `gen`erates typed, allocation-light Go decoders from them.                                                                |

Every exported identifier also has a doc comment; running `go doc` against the module (e.g.
`go doc ./foxglove` or `go doc ./foxglove Client.Subscribe`) always reflects the code exactly. The
documents here add the narrative and worked examples that godoc comments intentionally leave out.
