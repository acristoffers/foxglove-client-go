# Decoding messages

`foxglove.MessageData.Data` is an opaque `[]byte` in whatever encoding the channel declares
(`Channel.Encoding`): `"json"` payloads decode with `encoding/json` and need nothing from this
library. ROS 2 channels normally use `"cdr"`, the OMG Common Data Representation — that's what this
document covers, with the `cdr` and `ros2msg` packages.

There are two ways to decode a CDR payload:

1. **Generic, no code generation** — parse the channel's `ros2msg` schema at runtime with
   `ros2msg.Parse`, then `Schema.Decode` each payload into a `*ros2msg.Message` tree. Works for any
   message type, including ones you only learn about at runtime (e.g. a generic topic browser).
   Slower and less type-safe, since every field is accessed through `any`.
2. **Generated, typed** — run the `msggen` tool once per message type you care about to produce a Go
   struct and a hand-specialized decoder. Faster, type-safe, and `go vet`-checkable, at the cost of
   a codegen step. See [codegen.md](codegen.md).

Pick generic decoding for exploratory tools and anything that must handle arbitrary/unknown types;
pick generated decoding for anything that repeatedly processes known message types, e.g. a logger or
a bridge into another system.

## Generic decoding with `ros2msg`

A channel's schema arrives as text in the `ros2msg` encoding: the `.msg` body of the root type,
followed by every type it depends on, each introduced by a line of `=` and a `MSG: pkg/Type` header
— the same format `foxglove_bridge` advertises and MCAP files embed.

```go
ch, _ := client.ChannelByTopic("/odom")
schemaText, err := ch.SchemaData()
if err != nil {
	log.Fatal(err)
}

schema, err := ros2msg.Parse(ch.SchemaName, string(schemaText))
if err != nil {
	log.Fatal(err)
}
```

`ros2msg.Parse` validates the whole schema up front: every referenced type must be defined, and no
type may contain itself (directly or transitively) — so a successfully parsed `*ros2msg.Schema` can
be decoded against without further error-checking of the *shape*, only of the *data*.

Decode payloads from `MessageData` events against the parsed schema:

```go
case foxglove.MessageData:
	if ev.Channel.Topic != "/odom" {
		break
	}
	msg, err := schema.Decode(ev.Data)
	if err != nil {
		log.Println("decode:", err)
		break
	}
	x, _ := msg.Get("pose", "pose", "position", "x")
	fmt.Println("x =", x)
```

`*ros2msg.Message` is a tree: `Message.Fields` is `[]FieldValue` in declaration order, and each
`FieldValue.Value` is one of `bool`, an integer type (`int8`..`uint64`), `float32`, `float64`,
`string`, `*ros2msg.Message` (a nested message), or `[]any` of any of those (an array/sequence
field). `Message.Get(path...)` walks nested fields by name in one call, as shown above, returning
`(nil, false)` if any step doesn't exist.

For logging or debugging, `Message.YAML()` formats a message exactly the way `rosidl`'s `to_yaml`
does — the same format ROS nodes print in their logs — which makes decoded output diffable against
what you'd see in `ros2 topic echo`:

```go
fmt.Print(msg.YAML())
```

### Reusing one schema per channel

Parse each channel's schema once (e.g. when handling its `Advertise` event) and keep the
`*ros2msg.Schema` around keyed by channel ID or topic — don't reparse the schema text on every
`MessageData`. Parsing involves resolving and validating every type in the schema (not just the
root), which is wasted work to repeat per message:

```go
schemas := map[uint64]*ros2msg.Schema{} // channel ID -> schema

case foxglove.Advertise:
	for _, ch := range ev.Channels {
		if ch.Encoding != "cdr" {
			continue
		}
		text, err := ch.SchemaData()
		if err != nil {
			continue
		}
		schema, err := ros2msg.Parse(ch.SchemaName, string(text))
		if err != nil {
			log.Printf("%s: %v", ch.Topic, err)
			continue
		}
		schemas[ch.ID] = schema
	}

case foxglove.MessageData:
	if schema := schemas[ev.Channel.ID]; schema != nil {
		msg, err := schema.Decode(ev.Data)
		// ...
	}
```

### Other `ros2msg.Schema` operations

- `schema.Dependencies(typeName)` — the fully qualified names of every type a definition uses,
  directly or transitively, sorted. Useful for printing a schema's full type closure or deciding
  what to fetch/generate.
- `schema.Fingerprint(typeName)` — a short hex digest of a type's wire layout (field names and
  types, recursively), ignoring comments, whitespace, defaults and constants. Two definitions with
  the same fingerprint serialize identically; `msggen`-generated code embeds this so programs can
  detect when a running system's message layout no longer matches the code that was generated
  against it.
- `def.ConstantName(prefix, value)` — looks up an enum-like constant by its value, e.g. turning a
  raw `uint8` status code back into the name ROS gave it (`STATE_RISING`, `STATE_FALLING`, ...) for
  display.
- `ros2msg.Normalize(name)` — turns `"geometry_msgs/msg/Point"` into `"geometry_msgs/Point"`.
  `Parse` and `Schema` methods normalize names internally, but you'll want this if you're comparing
  a `Channel.SchemaName` (which includes `/msg/`) against names you've stored elsewhere without it.

## The `cdr` package

Most code never touches `cdr` directly — `ros2msg.Schema.Decode` and `msggen`-generated
`UnmarshalCDR` methods use it internally. Reach for it yourself only if you're decoding a message
type ad hoc, without going through either of the above (e.g. a one-off script, or a type `ros2msg`
can't represent).

```go
r, err := cdr.NewReader(payload) // payload starts with the 4-byte encapsulation header
if err != nil {
	log.Fatal(err)
}
x := r.Float64()
y := r.Float64()
name := r.String()
if err := r.Err(); err != nil {
	log.Fatal(err) // check once at the end, not after every read
}
```

`cdr.Reader`'s errors are sticky: once a read fails, every subsequent read returns the zero value
instead of also erroring, so a decoder can read a whole message field-by-field in sequence and check
`Err()` exactly once at the end — the pattern every generated `decode` method and
`ros2msg.decodeMessage` follows. `Reader` handles alignment and both XCDR1 and plain XCDR2
encapsulations (big- and little-endian) automatically; mutable/appendable extensibility is not
supported, which is fine because ROS 2 message types are always final.

Key methods: `Bool`, `Uint8`/`Int8`, `Uint16`/`Int16`, `Uint32`/`Int32`, `Uint64`/`Int64`,
`Float32`, `Float64`, `String` (length-prefixed, NUL stripped), `Bytes(n)` (raw bytes, returns a
copy), and `SequenceLength(minElemSize)` for the element count prefixing a sequence — it rejects
counts that couldn't possibly fit in the remaining data, so corrupt input can't trigger a huge
allocation.
