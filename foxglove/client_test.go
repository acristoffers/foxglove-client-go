package foxglove

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

const timeout = 2 * time.Second

// connect starts a fake server that greets clients with info, dials it, and
// returns both ends of the connection.
func connect(t *testing.T, info string, subprotocols ...string) (*Client, *websocket.Conn) {
	t.Helper()
	url, conns := fakeServer(t, info, subprotocols...)

	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	client, err := Dial(ctx, url, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	server := <-conns
	// Cleanups run last-in first-out: dropping the server end first keeps
	// client.Close from waiting on a close handshake nobody answers.
	t.Cleanup(func() { server.CloseNow() })

	return client, server
}

func fakeServer(t *testing.T, info string, subprotocols ...string) (string, <-chan *websocket.Conn) {
	t.Helper()
	if len(subprotocols) == 0 {
		subprotocols = []string{SubprotocolSDK}
	}
	conns := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: subprotocols})
		if err != nil {
			return
		}
		if conn.Subprotocol() == "" {
			conn.Close(websocket.StatusProtocolError, "unsupported subprotocol")
			return
		}
		conn.SetReadLimit(-1)
		if err := conn.Write(context.Background(), websocket.MessageText, []byte(info)); err != nil {
			return
		}
		conns <- conn
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http"), conns
}

func serverInfo(capabilities ...Capability) string {
	data, _ := json.Marshal(map[string]any{
		"op":                 "serverInfo",
		"name":               "fake",
		"capabilities":       capabilities,
		"supportedEncodings": []string{"cdr", "json"},
		"sessionId":          "1234",
	})
	return string(data)
}

func send(t *testing.T, conn *websocket.Conn, msg string) {
	t.Helper()
	if err := conn.Write(t.Context(), websocket.MessageText, []byte(msg)); err != nil {
		t.Fatalf("server write: %v", err)
	}
}

func sendBinary(t *testing.T, conn *websocket.Conn, msg []byte) {
	t.Helper()
	if err := conn.Write(t.Context(), websocket.MessageBinary, msg); err != nil {
		t.Fatalf("server write: %v", err)
	}
}

func receive(t *testing.T, conn *websocket.Conn) (websocket.MessageType, []byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	typ, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("server read: %v", err)
	}
	return typ, data
}

func receiveJSON(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	typ, data := receive(t, conn)
	if typ != websocket.MessageText {
		t.Fatalf("server got a binary message, want text")
	}
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("server got invalid JSON %q: %v", data, err)
	}
	return msg
}

func receiveBinary(t *testing.T, conn *websocket.Conn) []byte {
	t.Helper()
	typ, data := receive(t, conn)
	if typ != websocket.MessageBinary {
		t.Fatalf("server got text message %q, want binary", data)
	}
	return data
}

// nextEvent returns the next event, which must be of type T.
func nextEvent[T Event](t *testing.T, c *Client) T {
	t.Helper()
	select {
	case ev, ok := <-c.Events():
		if !ok {
			t.Fatalf("events closed while waiting for %T: %v", *new(T), c.Err())
		}
		got, ok := ev.(T)
		if !ok {
			t.Fatalf("got event %#v, want %T", ev, *new(T))
		}
		return got
	case <-time.After(timeout):
		t.Fatalf("timed out waiting for %T", *new(T))
	}
	panic("unreachable")
}

func messageData(subID uint32, timestamp uint64, payload string) []byte {
	buf := []byte{opMessageData}
	buf = binary.LittleEndian.AppendUint32(buf, subID)
	buf = binary.LittleEndian.AppendUint64(buf, timestamp)
	return append(buf, payload...)
}

const twoChannels = `{"op":"advertise","channels":[
	{"id":2,"topic":"/odom","encoding":"cdr","schemaName":"nav_msgs/msg/Odometry","schemaEncoding":"ros2msg","schema":"std_msgs/Header header"},
	{"id":1,"topic":"/cmd_vel","encoding":"cdr","schemaName":"geometry_msgs/msg/Twist","schemaEncoding":"ros2msg","schema":"Vector3 linear\nVector3 angular"}
]}`

func TestDial(t *testing.T) {
	client, _ := connect(t, serverInfo(CapabilityServices, CapabilityTime))

	info := nextEvent[ServerInfo](t, client)
	if info.Name != "fake" || info.SessionID != "1234" {
		t.Errorf("server info = %+v", info)
	}
	if !info.Has(CapabilityServices) || info.Has(CapabilityAssets) {
		t.Errorf("capabilities = %v", info.Capabilities)
	}
	if got := client.ServerInfo().Name; got != "fake" {
		t.Errorf("ServerInfo().Name = %q", got)
	}
	if got := client.Subprotocol(); got != SubprotocolSDK {
		t.Errorf("Subprotocol() = %q, want %q", got, SubprotocolSDK)
	}
}

func TestDialLegacySubprotocol(t *testing.T) {
	client, _ := connect(t, serverInfo(), SubprotocolLegacy)
	if got := client.Subprotocol(); got != SubprotocolLegacy {
		t.Errorf("Subprotocol() = %q, want %q", got, SubprotocolLegacy)
	}
}

func TestDialUnsupportedSubprotocol(t *testing.T) {
	url, _ := fakeServer(t, serverInfo(), "foxglove.websocket.v2")
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	if _, err := Dial(ctx, url, nil); err == nil {
		t.Fatal("Dial succeeded without a common subprotocol")
	}
}

func TestDialRequiresServerInfo(t *testing.T) {
	url, _ := fakeServer(t, `{"op":"status","level":0,"message":"hi"}`)
	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()
	if _, err := Dial(ctx, url, nil); err == nil {
		t.Fatal("Dial succeeded without server info")
	}
}

func TestChannels(t *testing.T) {
	client, server := connect(t, serverInfo())
	nextEvent[ServerInfo](t, client)

	send(t, server, twoChannels)
	adv := nextEvent[Advertise](t, client)
	if len(adv.Channels) != 2 {
		t.Fatalf("advertised %d channels, want 2", len(adv.Channels))
	}

	channels := client.Channels()
	if len(channels) != 2 || channels[0].Topic != "/cmd_vel" || channels[1].Topic != "/odom" {
		t.Errorf("Channels() not sorted by topic: %v, %v", channels[0], channels[1])
	}
	ch, ok := client.ChannelByTopic("/odom")
	if !ok || ch.ID != 2 || ch.SchemaName != "nav_msgs/msg/Odometry" {
		t.Errorf("ChannelByTopic(/odom) = %+v, %v", ch, ok)
	}

	send(t, server, `{"op":"unadvertise","channelIds":[2]}`)
	nextEvent[Unadvertise](t, client)
	if _, ok := client.Channel(2); ok {
		t.Error("channel 2 still known after unadvertise")
	}
	if _, ok := client.Channel(1); !ok {
		t.Error("channel 1 lost after unadvertising channel 2")
	}
}

func TestSubscribe(t *testing.T) {
	client, server := connect(t, serverInfo())
	nextEvent[ServerInfo](t, client)
	send(t, server, twoChannels)
	nextEvent[Advertise](t, client)

	subID, err := client.Subscribe(2)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	msg := receiveJSON(t, server)
	want := fmt.Sprintf(`[map[channelId:2 id:%d]]`, subID)
	if msg["op"] != "subscribe" || fmt.Sprint(msg["subscriptions"]) != want {
		t.Errorf("subscribe message = %v", msg)
	}

	again, err := client.Subscribe(2)
	if err != nil || again != subID {
		t.Errorf("second Subscribe = %d, %v; want existing subscription %d", again, err, subID)
	}

	if _, err := client.Subscribe(42); err == nil {
		t.Error("Subscribe to an unknown channel succeeded")
	}

	sendBinary(t, server, messageData(subID+100, 1, "unknown subscription"))
	sendBinary(t, server, messageData(subID, 1_700_000_000_000_000_000, "payload"))
	data := nextEvent[MessageData](t, client)
	if data.SubscriptionID != subID || data.Timestamp != 1_700_000_000_000_000_000 || string(data.Data) != "payload" {
		t.Errorf("message data = %+v", data)
	}
	if data.Channel == nil || data.Channel.Topic != "/odom" {
		t.Errorf("message data channel = %+v", data.Channel)
	}

	if err := client.Unsubscribe(subID); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	// Receiving the unsubscribe right away also proves the second Subscribe
	// sent nothing.
	msg = receiveJSON(t, server)
	if msg["op"] != "unsubscribe" || fmt.Sprint(msg["subscriptionIds"]) != fmt.Sprintf("[%d]", subID) {
		t.Errorf("unsubscribe message = %v", msg)
	}

	// In-flight messages after an unsubscribe are dropped.
	sendBinary(t, server, messageData(subID, 2, "late"))
	send(t, server, `{"op":"status","level":1,"message":"marker"}`)
	if status := nextEvent[Status](t, client); status.Level != StatusWarning || status.Message != "marker" {
		t.Errorf("status = %+v", status)
	}
}

func TestUnadvertiseEndsSubscription(t *testing.T) {
	client, server := connect(t, serverInfo())
	nextEvent[ServerInfo](t, client)
	send(t, server, twoChannels)
	nextEvent[Advertise](t, client)

	subID, err := client.Subscribe(1)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	receiveJSON(t, server)

	send(t, server, `{"op":"unadvertise","channelIds":[1]}`)
	nextEvent[Unadvertise](t, client)
	sendBinary(t, server, messageData(subID, 1, "stale"))
	send(t, server, `{"op":"status","level":0,"message":"marker"}`)
	nextEvent[Status](t, client)

	// The channel comes back and is subscribed to anew.
	send(t, server, twoChannels)
	nextEvent[Advertise](t, client)
	newID, err := client.Subscribe(1)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if msg := receiveJSON(t, server); msg["op"] != "subscribe" {
		t.Errorf("expected a new subscribe message, got %v", msg)
	}
	sendBinary(t, server, messageData(newID, 1, "fresh"))
	if data := nextEvent[MessageData](t, client); string(data.Data) != "fresh" {
		t.Errorf("message data = %q", data.Data)
	}
}

func TestCallService(t *testing.T) {
	client, server := connect(t, serverInfo(CapabilityServices))
	nextEvent[ServerInfo](t, client)
	send(t, server, `{"op":"advertiseServices","services":[{
		"id":7,"name":"/set_bool","type":"std_srvs/srv/SetBool",
		"request":{"encoding":"cdr","schemaName":"std_srvs/srv/SetBool_Request","schemaEncoding":"ros2msg","schema":"bool data"},
		"response":{"encoding":"cdr","schemaName":"std_srvs/srv/SetBool_Response","schemaEncoding":"ros2msg","schema":"bool success\nstring message"}
	}]}`)
	nextEvent[AdvertiseServices](t, client)

	svc, ok := client.ServiceByName("/set_bool")
	if !ok || svc.ID != 7 || svc.Request == nil || svc.Request.SchemaName != "std_srvs/srv/SetBool_Request" {
		t.Fatalf("ServiceByName = %+v, %v", svc, ok)
	}

	type result struct {
		resp ServiceCallResponse
		err  error
	}
	call := func() <-chan result {
		results := make(chan result, 1)
		go func() {
			resp, err := client.CallService(t.Context(), 7, "", []byte{1})
			results <- result{resp, err}
		}()
		return results
	}

	results := call()
	req := receiveBinary(t, server)
	le := binary.LittleEndian
	callID := le.Uint32(req[5:9])
	encodingLen := le.Uint32(req[9:13])
	if req[0] != opServiceCallRequest || le.Uint32(req[1:5]) != 7 || string(req[13:13+encodingLen]) != "cdr" || string(req[13+encodingLen:]) != "\x01" {
		t.Fatalf("service call request = %x", req)
	}

	resp := []byte{opServiceCallResponse}
	resp = le.AppendUint32(resp, 7)
	resp = le.AppendUint32(resp, callID)
	resp = le.AppendUint32(resp, 3)
	resp = append(resp, "cdrok"...)
	sendBinary(t, server, resp)

	r := <-results
	if r.err != nil {
		t.Fatalf("CallService: %v", r.err)
	}
	if r.resp.CallID != callID || r.resp.Encoding != "cdr" || string(r.resp.Payload) != "ok" {
		t.Errorf("response = %+v", r.resp)
	}

	results = call()
	callID = le.Uint32(receiveBinary(t, server)[5:9])
	send(t, server, fmt.Sprintf(`{"op":"serviceCallFailure","serviceId":7,"callId":%d,"message":"nope"}`, callID))
	r = <-results
	var callErr *ServiceCallError
	if !errors.As(r.err, &callErr) || callErr.Message != "nope" {
		t.Errorf("CallService error = %v, want ServiceCallError", r.err)
	}

	if _, err := client.CallService(t.Context(), 99, "cdr", nil); err == nil {
		t.Error("calling an unknown service succeeded")
	}

	send(t, server, `{"op":"unadvertiseServices","serviceIds":[7]}`)
	nextEvent[UnadvertiseServices](t, client)
	if len(client.Services()) != 0 {
		t.Errorf("services left after unadvertise: %v", client.Services())
	}
}

func TestCallServiceContext(t *testing.T) {
	client, server := connect(t, serverInfo(CapabilityServices))
	send(t, server, `{"op":"advertiseServices","services":[{"id":1,"name":"/slow","type":"std_srvs/srv/Trigger","requestSchema":"","responseSchema":""}]}`)
	nextEvent[ServerInfo](t, client)
	nextEvent[AdvertiseServices](t, client)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if _, err := client.CallService(ctx, 1, "cdr", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("CallService = %v, want deadline exceeded", err)
	}
	// The connection survives an abandoned call.
	receiveBinary(t, server)
	send(t, server, `{"op":"status","level":0,"message":"still here"}`)
	nextEvent[Status](t, client)
}

func TestParameters(t *testing.T) {
	client, server := connect(t, serverInfo(CapabilityParameters, CapabilityParametersSubscribe))
	nextEvent[ServerInfo](t, client)

	type result struct {
		params []Parameter
		err    error
	}
	results := make(chan result, 1)
	go func() {
		params, err := client.GetParameters(t.Context(), "/a", "/b")
		results <- result{params, err}
	}()

	msg := receiveJSON(t, server)
	if msg["op"] != "getParameters" || fmt.Sprint(msg["parameterNames"]) != "[/a /b]" {
		t.Fatalf("get parameters message = %v", msg)
	}
	id := msg["id"].(string)

	// An update without an id arrives first and goes to Events.
	send(t, server, `{"op":"parameterValues","parameters":[{"name":"/a","value":1}]}`)
	send(t, server, fmt.Sprintf(`{"op":"parameterValues","id":%q,"parameters":[
		{"name":"/int","value":3},
		{"name":"/float","value":3,"type":"float64"},
		{"name":"/bytes","value":%q,"type":"byte_array"},
		{"name":"/list","value":[1,2]},
		{"name":"/flag","value":true},
		{"name":"/unset"}
	]}`, id, base64.StdEncoding.EncodeToString([]byte("raw"))))

	update := nextEvent[ParameterValues](t, client)
	if len(update.Parameters) != 1 || update.Parameters[0].Name != "/a" {
		t.Errorf("parameter update = %+v", update)
	}

	r := <-results
	if r.err != nil {
		t.Fatalf("GetParameters: %v", r.err)
	}
	params := map[string]Parameter{}
	for _, p := range r.params {
		params[p.Name] = p
	}
	if v, ok := params["/int"].AsInt(); !ok || v != 3 {
		t.Errorf("/int = %v, %v", v, ok)
	}
	if v, ok := params["/float"].AsFloat64(); !ok || v != 3 || params["/float"].Type != ParameterFloat64 {
		t.Errorf("/float = %v, %v", v, ok)
	}
	if v, err := params["/bytes"].AsBytes(); err != nil || string(v) != "raw" {
		t.Errorf("/bytes = %q, %v", v, err)
	}
	if v, ok := params["/list"].Value.([]any); !ok || len(v) != 2 {
		t.Errorf("/list = %#v", params["/list"].Value)
	}
	if v, ok := params["/flag"].AsBool(); !ok || !v {
		t.Errorf("/flag = %v, %v", v, ok)
	}
	if params["/unset"].IsSet() {
		t.Errorf("/unset has value %v", params["/unset"].Value)
	}

	go func() {
		params, err := client.SetParameters(t.Context(),
			Float64Parameter("/f", 2),
			IntParameter("/i", 2),
			BoolParameter("/b", false),
			UnsetParameter("/gone"),
		)
		results <- result{params, err}
	}()
	_, data := receive(t, server)
	wantJSON := `"parameters":[{"name":"/f","type":"float64","value":2},{"name":"/i","value":2},{"name":"/b","value":false},{"name":"/gone"}]`
	if !strings.Contains(string(data), wantJSON) {
		t.Errorf("set parameters message = %s, want it to contain %s", data, wantJSON)
	}
	var set struct{ ID string }
	json.Unmarshal(data, &set)
	send(t, server, fmt.Sprintf(`{"op":"parameterValues","id":%q,"parameters":[{"name":"/f","value":2,"type":"float64"}]}`, set.ID))
	if r := <-results; r.err != nil || len(r.params) != 1 {
		t.Errorf("SetParameters = %+v, %v", r.params, r.err)
	}

	if err := client.SubscribeParameterUpdates(); err != nil {
		t.Fatalf("SubscribeParameterUpdates: %v", err)
	}
	if msg := receiveJSON(t, server); msg["op"] != "subscribeParameterUpdates" || fmt.Sprint(msg["parameterNames"]) != "[]" {
		t.Errorf("subscribe parameter updates message = %v", msg)
	}
}

func TestFetchAsset(t *testing.T) {
	client, server := connect(t, serverInfo(CapabilityAssets))
	nextEvent[ServerInfo](t, client)

	type result struct {
		data []byte
		err  error
	}
	fetch := func(uri string) <-chan result {
		results := make(chan result, 1)
		go func() {
			data, err := client.FetchAsset(t.Context(), uri)
			results <- result{data, err}
		}()
		return results
	}
	reply := func(requestID uint32, status byte, message, data string) []byte {
		buf := []byte{opFetchAssetResponse}
		buf = binary.LittleEndian.AppendUint32(buf, requestID)
		buf = append(buf, status)
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(message)))
		buf = append(buf, message...)
		return append(buf, data...)
	}

	results := fetch("package://robot/robot.urdf")
	msg := receiveJSON(t, server)
	if msg["op"] != "fetchAsset" || msg["uri"] != "package://robot/robot.urdf" {
		t.Fatalf("fetch asset message = %v", msg)
	}
	sendBinary(t, server, reply(uint32(msg["requestId"].(float64)), 0, "", "<robot/>"))
	if r := <-results; r.err != nil || string(r.data) != "<robot/>" {
		t.Errorf("FetchAsset = %q, %v", r.data, r.err)
	}

	results = fetch("package://robot/missing")
	msg = receiveJSON(t, server)
	sendBinary(t, server, reply(uint32(msg["requestId"].(float64)), 1, "not found", ""))
	var assetErr *AssetError
	if r := <-results; !errors.As(r.err, &assetErr) || assetErr.Message != "not found" {
		t.Errorf("FetchAsset error = %v, want AssetError", r.err)
	}
}

func TestPublish(t *testing.T) {
	client, server := connect(t, serverInfo(CapabilityClientPublish))
	nextEvent[ServerInfo](t, client)

	id, err := client.Advertise(ClientChannel{
		Topic:          "/cmd",
		Encoding:       "protobuf",
		SchemaName:     "Cmd",
		SchemaEncoding: "protobuf",
		Schema:         []byte{0xde, 0xad},
	})
	if err != nil {
		t.Fatalf("Advertise: %v", err)
	}
	msg := receiveJSON(t, server)
	channels := msg["channels"].([]any)
	ch := channels[0].(map[string]any)
	if msg["op"] != "advertise" || ch["topic"] != "/cmd" || ch["schema"] != "3q0=" || uint32(ch["id"].(float64)) != id {
		t.Errorf("advertise message = %v", msg)
	}

	if err := client.Publish(id, []byte("hello")); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	data := receiveBinary(t, server)
	if data[0] != opClientMessageData || binary.LittleEndian.Uint32(data[1:5]) != id || string(data[5:]) != "hello" {
		t.Errorf("client message data = %x", data)
	}

	if err := client.Unadvertise(id); err != nil {
		t.Fatalf("Unadvertise: %v", err)
	}
	if msg := receiveJSON(t, server); msg["op"] != "unadvertise" {
		t.Errorf("unadvertise message = %v", msg)
	}
	if err := client.Publish(id, nil); err == nil {
		t.Error("Publish on an unadvertised channel succeeded")
	}
}

func TestMissingCapability(t *testing.T) {
	client, _ := connect(t, serverInfo())

	checks := map[string]error{
		"CallService":               func() error { _, err := client.CallService(t.Context(), 1, "cdr", nil); return err }(),
		"GetParameters":             func() error { _, err := client.GetParameters(t.Context()); return err }(),
		"SubscribeParameterUpdates": client.SubscribeParameterUpdates(),
		"SubscribeConnectionGraph":  client.SubscribeConnectionGraph(),
		"FetchAsset":                func() error { _, err := client.FetchAsset(t.Context(), "x"); return err }(),
		"Advertise":                 func() error { _, err := client.Advertise(ClientChannel{}); return err }(),
	}
	for name, err := range checks {
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s = %v, want ErrUnsupported", name, err)
		}
	}
}

func TestMalformedMessage(t *testing.T) {
	client, server := connect(t, serverInfo())
	nextEvent[ServerInfo](t, client)

	send(t, server, `{"op":"advertise","channels":"oops"}`)
	sendBinary(t, server, []byte{opMessageData, 1})
	sendBinary(t, server, []byte{0x7f, 1, 2, 3}) // unknown opcode, ignored
	send(t, server, `{"op":"somethingNew"}`)     // unknown op, ignored
	send(t, server, `{"op":"status","level":2,"message":"still connected"}`)

	nextEvent[ProtocolError](t, client)
	nextEvent[ProtocolError](t, client)
	if status := nextEvent[Status](t, client); status.Level != StatusError {
		t.Errorf("status = %+v", status)
	}
}

func TestConnectionGraph(t *testing.T) {
	client, server := connect(t, serverInfo(CapabilityConnectionGraph))
	nextEvent[ServerInfo](t, client)

	if err := client.SubscribeConnectionGraph(); err != nil {
		t.Fatalf("SubscribeConnectionGraph: %v", err)
	}
	if msg := receiveJSON(t, server); msg["op"] != "subscribeConnectionGraph" {
		t.Errorf("subscribe connection graph message = %v", msg)
	}

	send(t, server, `{"op":"connectionGraphUpdate",
		"publishedTopics":[{"name":"/odom","publisherIds":["/ekf"]}],
		"subscribedTopics":[{"name":"/odom","subscriberIds":["/planner"]}],
		"advertisedServices":[{"name":"/reset","providerIds":["/ekf"]}],
		"removedTopics":["/old"],"removedServices":[]}`)
	update := nextEvent[ConnectionGraphUpdate](t, client)
	if update.PublishedTopics[0].PublisherIDs[0] != "/ekf" || update.SubscribedTopics[0].SubscriberIDs[0] != "/planner" ||
		update.AdvertisedServices[0].ProviderIDs[0] != "/ekf" || update.RemovedTopics[0] != "/old" {
		t.Errorf("connection graph update = %+v", update)
	}
}

func TestServerTime(t *testing.T) {
	client, server := connect(t, serverInfo(CapabilityTime))
	nextEvent[ServerInfo](t, client)

	sendBinary(t, server, binary.LittleEndian.AppendUint64([]byte{opTime}, 42))
	if tm := nextEvent[Time](t, client); tm.Timestamp != 42 {
		t.Errorf("time = %d", tm.Timestamp)
	}
}

func TestClose(t *testing.T) {
	client, server := connect(t, serverInfo())
	go func() {
		// Reading lets the server answer the close handshake.
		for {
			if _, _, err := server.Read(context.Background()); err != nil {
				return
			}
		}
	}()

	if err := client.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	if !errors.Is(client.Err(), ErrClosed) {
		t.Errorf("Err() = %v, want ErrClosed", client.Err())
	}
	for range client.Events() {
		// Drain the ServerInfo; the loop ending proves Events is closed.
	}
	if err := client.SubscribeParameterUpdates(); err == nil {
		t.Error("request succeeded on a closed client")
	}
}

func TestServerDisconnect(t *testing.T) {
	client, server := connect(t, serverInfo(CapabilityServices))
	send(t, server, `{"op":"advertiseServices","services":[{"id":1,"name":"/s","type":"t","requestSchema":"","responseSchema":""}]}`)
	nextEvent[ServerInfo](t, client)
	nextEvent[AdvertiseServices](t, client)

	errs := make(chan error, 1)
	go func() {
		_, err := client.CallService(t.Context(), 1, "cdr", nil)
		errs <- err
	}()
	receiveBinary(t, server)
	server.CloseNow()

	select {
	case err := <-errs:
		if err == nil || errors.Is(err, ErrClosed) {
			t.Errorf("pending call error = %v, want connection lost", err)
		}
	case <-time.After(timeout):
		t.Fatal("pending call not released when the server went away")
	}
	select {
	case <-client.Done():
	case <-time.After(timeout):
		t.Fatal("client not done after the server went away")
	}
	if err := client.Err(); err == nil || errors.Is(err, ErrClosed) {
		t.Errorf("Err() = %v, want connection lost", err)
	}
	if err := client.Close(); err != nil {
		t.Errorf("Close after disconnect: %v", err)
	}
}
