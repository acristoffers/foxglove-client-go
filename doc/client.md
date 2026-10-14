# Client API

This document covers every capability of `foxglove.Client`. See
[Getting started](getting-started.md) first if you haven't connected a client yet.

All `*Client` methods are safe for concurrent use. Typical programs run the `Events()` drain loop in
one goroutine and call methods like `Subscribe` or `CallService` from others (e.g. in response to a
UI action).

## Server capabilities

Not every server supports every feature below. The server advertises what it supports in
`ServerInfo.Capabilities`, available via `client.ServerInfo()`:

```go
info := client.ServerInfo()
if info.Has(foxglove.CapabilityServices) {
	// safe to call client.CallService
}
```

Methods that need a capability the server didn't advertise return `foxglove.ErrUnsupported` (wrapped
with which capability was missing) instead of attempting the request — check with
`errors.Is(err, foxglove.ErrUnsupported)` if you want to handle that case specifically, e.g. to fall
back to a different strategy.

The capabilities are: `CapabilityClientPublish`, `CapabilityParameters`,
`CapabilityParametersSubscribe`, `CapabilityTime`, `CapabilityServices`,
`CapabilityConnectionGraph`, `CapabilityAssets`, `CapabilityPlaybackControl` (the last is advertised
by some servers for playback/seek controls, but `Client` has no methods for it yet — issue requests
for it manually with `CallService`/raw messages if you need it).

## Channels and subscribing

The server advertises the channels (topics) it can stream as `foxglove.Advertise` events, each
carrying one or more `*foxglove.Channel`. The client keeps track of the current set, so you don't
have to accumulate them yourself:

```go
for _, ch := range client.Channels() { // sorted by topic
	fmt.Println(ch.ID, ch.Topic, ch.SchemaName)
}

ch, ok := client.ChannelByTopic("/odom")
ch, ok  = client.Channel(42) // by ID
```

`Channel.Schema` is the schema in its wire encoding — text for `ros2msg`, `ros2idl`, `jsonschema`,
etc., base64 for binary encodings like `protobuf` and `flatbuffer`. Call `Channel.SchemaData()` to
get it decoded either way:

```go
schemaText, err := ch.SchemaData()
```

Subscribe to start receiving a channel's messages as `foxglove.MessageData` events:

```go
subID, err := client.Subscribe(ch.ID)
```

`Subscribe` is idempotent per channel — subscribing twice returns the existing subscription ID
instead of creating a second one. A subscription ends when the server unadvertises the channel
(`foxglove.Unadvertise`); if the topic comes back later with a new `Advertise`, it needs a fresh
`Subscribe` call, since the old subscription ID is gone. `client.Unsubscribe (subID)` ends a
subscription early; unsubscribing an ID that no longer exists is a no-op, not an error.

`MessageData` events carry the subscription they belong to and the channel describing their payload:

```go
case foxglove.MessageData:
	fmt.Println(ev.Channel.Topic, ev.Timestamp, len(ev.Data))
	// ev.Data is in ev.Channel.Encoding — "cdr" for ROS 2, see decoding.md
```

`ev.Channel` is shared with the client's internal state and other events — read it, don't mutate it.

## Publishing

Publishing requires `CapabilityClientPublish`. Announce a channel, then send messages on it:

```go
chID, err := client.Advertise(foxglove.ClientChannel{
	Topic:          "/goal",
	Encoding:       "json", // must be one of ServerInfo.SupportedEncodings
	SchemaName:     "geometry_msgs/msg/Point",
	SchemaEncoding: "jsonschema",
	Schema:         goalSchemaBytes,
})
if err != nil {
	log.Fatal(err)
}

payload, _ := json.Marshal(map[string]float64{"x": 1, "y": 2, "z": 0})
if err := client.Publish(chID, payload); err != nil {
	log.Println("publish:", err)
}

// ... later, if you're done:
client.Unadvertise(chID)
```

`Advertise` returns a client-local channel ID — distinct from server channel IDs — used only with
`Publish` and `Unadvertise`. `Schema` is raw bytes; the client base64-encodes it on the wire itself
when the schema encoding is binary (`protobuf`, `flatbuffer`), so pass the schema as-is regardless
of encoding.

## Calling services

Calling services requires `CapabilityServices`. List what's advertised the same way as channels:

```go
for _, svc := range client.Services() { // sorted by name
	fmt.Println(svc.ID, svc.Name, svc.Type)
}
svc, ok := client.ServiceByName("/reset")
```

Call one and wait for the reply:

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

resp, err := client.CallService(ctx, svc.ID, "", requestPayload)
var svcErr *foxglove.ServiceCallError
switch {
case errors.As(err, &svcErr):
	log.Printf("service reported failure: %s", svcErr.Message)
case err != nil:
	log.Fatal(err) // transport/context error
default:
	fmt.Println(resp.Encoding, len(resp.Payload))
}
```

The `encoding` argument may be left as `""` to use the encoding the service declared for its
requests (`svc.Request.Encoding`); pass it explicitly only when a server didn't describe its request
schema and you know the encoding out of band. `ctx` bounds the call: it's cancelled if the context
is done or if the connection ends before a reply arrives, whichever comes first.

A failure the *server* reports (as opposed to a transport failure) comes back as
`*foxglove.ServiceCallError`, which carries the service and call IDs plus the server's message — use
`errors.As` to distinguish it from connection-level errors, as shown above.

## Parameters

Reading and writing parameters requires `CapabilityParameters`. `Parameter.Value` holds the decoded
value as `any` — one of `nil` (unset), `bool`, `string`, `json.Number`, `[]any`, or `map[string]any`
— so use the accessor methods rather than asserting the type directly:

```go
params, err := client.GetParameters(ctx) // all parameters
params, err  = client.GetParameters(ctx, "robot.max_speed", "robot.name")

for _, p := range params {
	if f, ok := p.AsFloat64(); ok {
		fmt.Println(p.Name, "=", f)
	}
}
```

`AsBool`, `AsString`, `AsInt`, `AsFloat64` and `AsBytes` each return a second `bool`/`error` telling
you whether the value was actually that type — `AsInt` deliberately fails for floats (even integral
ones) so you don't silently truncate a value the server considers fractional. `AsFloat64` converts
integers, matching how a JSON-based protocol can't always tell a whole-number float from an int on
the wire.

Setting parameters uses one of the constructor functions, which also stamp the `ParameterType` the
protocol needs for values JSON can't disambiguate on its own (floats vs. ints, raw bytes vs. text):

```go
updated, err := client.SetParameters(ctx,
	foxglove.Float64Parameter("robot.max_speed", 2.5),
	foxglove.StringParameter("robot.name", "robot-1"),
	foxglove.BoolArrayParameter("robot.flags", []bool{true, false}),
	foxglove.UnsetParameter("robot.old_param"), // removes it
)
```

`SetParameters` returns the resulting values as the server reports them back, which is the
authoritative way to confirm what was actually applied (a server may clamp, reject, or otherwise
transform a value). The full set of constructors: `BoolParameter`, `IntParameter`,
`Float64Parameter`, `StringParameter`, `ByteArrayParameter`, `IntArrayParameter`,
`Float64ArrayParameter`, `BoolArrayParameter`, `StringArrayParameter`, `UnsetParameter`.

To be notified when parameters change instead of polling, subscribe to updates (requires
`CapabilityParametersSubscribe`):

```go
client.SubscribeParameterUpdates("robot.max_speed") // or no names for "all"
// ...
case foxglove.ParameterValues:
	for _, p := range ev.Parameters {
		fmt.Println("changed:", p.Name)
	}
// ...
client.UnsubscribeParameterUpdates("robot.max_speed")
```

Note the asymmetry: `GetParameters`/`SetParameters` return their result directly from the call (they
are request/reply), while `SubscribeParameterUpdates` causes *future* changes to arrive as
`ParameterValues` events on `Events()` — the two never overlap, because the client only delivers a
`ParameterValues` as an event when it didn't correspond to a pending `Get`/`SetParameters` call.

## Assets

Fetching assets requires `CapabilityAssets`. Assets are files the server can serve by URI, typically
`package://pkg/path` for a ROS package resource such as a URDF:

```go
ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
defer cancel()

data, err := client.FetchAsset(ctx, "package://my_robot/urdf/robot.urdf")
var assetErr *foxglove.AssetError
if errors.As(err, &assetErr) {
	log.Printf("server could not provide %s: %s", assetErr.URI, assetErr.Message)
}
```

Same error-handling shape as `CallService`: a `*foxglove.AssetError` means the server understood the
request but couldn't fulfil it; any other error is a transport/context problem.

## Connection graph

Requires `CapabilityConnectionGraph`. This is ROS graph information (who
publishes/subscribes/provides what), independent of what the Foxglove protocol itself streams as
channels:

```go
client.SubscribeConnectionGraph()
// ...
case foxglove.ConnectionGraphUpdate:
	for _, t := range ev.PublishedTopics {
		fmt.Println(t.Name, "published by", t.PublisherIDs)
	}
	for _, name := range ev.RemovedTopics {
		fmt.Println(name, "no longer published")
	}
// ...
client.UnsubscribeConnectionGraph()
```

Each `ConnectionGraphUpdate` is incremental: entries in `PublishedTopics`, `SubscribedTopics` and
`AdvertisedServices` *replace* whatever was previously known about that topic/service, while
`RemovedTopics` and `RemovedServices` name things that disappeared. The client does not accumulate
graph state for you the way it does for channels and services — if you need the full current graph,
maintain it yourself from these deltas.

## Server time

If the server advertises `CapabilityTime`, it may send `foxglove.Time` events with its current
clock, and `MessageData.Timestamp` values use that same clock (rather than wall-clock receive time)
— relevant when replaying recorded data at a scale other than realtime.

## Shutting down

```go
err := client.Close()
```

`Close` is idempotent and blocks until the client's internal read loop has actually exited, so it's
safe to call from a deferred statement without a separate wait. Closing a connection the server
already ended is not an error. After `Close`, `Err()` returns `foxglove.ErrClosed`.

`client.Done()` returns a channel closed when the connection ends — by either side. Use it in a
`select` alongside your own cancellation signals if you need to react to a server-initiated
disconnect without necessarily reading every remaining event first:

```go
select {
case <-client.Done():
	log.Println("disconnected:", client.Err())
case <-stop:
	client.Close()
}
```
