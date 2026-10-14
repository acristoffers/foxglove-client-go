// Package foxglove implements a client for the Foxglove WebSocket protocol, as
// spoken by foxglove_bridge and other servers built on the Foxglove SDK.
//
// The wire format follows protocol v1 of the Foxglove SDK
// (rust/foxglove/src/protocol/v1). Servers built on the SDK negotiate the
// "foxglove.sdk.v1" subprotocol, while older ones (ws-protocol based bridges)
// negotiate "foxglove.websocket.v1". Both carry the same messages, and Dial
// offers both by default.
//
// A typical session:
//
//	client, err := foxglove.Dial(ctx, "ws://localhost:8765", nil)
//	if err != nil { ... }
//	defer client.Close()
//
//	for ev := range client.Events() {
//		switch ev := ev.(type) {
//		case foxglove.Advertise:
//			for _, ch := range ev.Channels {
//				if ch.Topic == "/odom" {
//					client.Subscribe(ch.ID)
//				}
//			}
//		case foxglove.MessageData:
//			// ev.Channel describes the payload in ev.Data (e.g. CDR + ros2msg).
//		}
//	}
//
// Every message the server sends is delivered on Events, except for replies to
// requests made through the client (service calls, parameter requests and
// asset fetches), which are returned by the method that made the request. The
// client also keeps track of the advertised channels and services, so the
// current set can be queried at any time.
//
// Events must be drained: the client reads from the socket only as fast as
// events are consumed, which lets the server's backpressure handling kick in
// when the consumer falls behind.
package foxglove
