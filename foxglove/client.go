package foxglove

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"sync"

	"github.com/coder/websocket"
)

var (
	// ErrClosed is returned once the client has been closed with Close.
	ErrClosed = errors.New("foxglove: client closed")
	// ErrUnsupported is returned when the server lacks the capability a
	// request needs.
	ErrUnsupported = errors.New("foxglove: not supported by server")
)

// ServiceCallError is returned by CallService when the server reports that
// the call failed.
type ServiceCallError struct {
	ServiceID uint32
	CallID    uint32
	Message   string
}

func (e *ServiceCallError) Error() string {
	return fmt.Sprintf("foxglove: service %d call %d failed: %s", e.ServiceID, e.CallID, e.Message)
}

// AssetError is returned by FetchAsset when the server could not provide the
// asset.
type AssetError struct {
	URI     string
	Message string
}

func (e *AssetError) Error() string {
	return fmt.Sprintf("foxglove: fetch asset %s: %s", e.URI, e.Message)
}

func (e ProtocolError) Error() string {
	return "foxglove: malformed server message: " + e.Err.Error()
}

func (e ProtocolError) Unwrap() error {
	return e.Err
}

// DialOptions configures Dial. The zero value is ready to use.
type DialOptions struct {
	// Subprotocols offered to the server, in order of preference. Defaults to
	// SubprotocolSDK and SubprotocolLegacy.
	Subprotocols []string
	// HTTPClient used for the handshake. Defaults to http.DefaultClient.
	HTTPClient *http.Client
	// HTTPHeader is added to the handshake request.
	HTTPHeader http.Header
	// EventBuffer is the capacity of the Events channel. Defaults to 1024.
	EventBuffer int
	// ReadLimit is the maximum size of a single incoming message, in bytes.
	// Zero means no limit, which is the sensible choice for images and point
	// clouds.
	ReadLimit int64
}

// Client is a connection to a Foxglove WebSocket server. It is safe for
// concurrent use.
type Client struct {
	conn        *websocket.Conn
	subprotocol string

	// ctx bounds every read and write, and is cancelled by Close.
	ctx    context.Context
	cancel context.CancelFunc

	events    chan Event
	closing   chan struct{} // closed when Close is called
	done      chan struct{} // closed when the read loop exits
	err       error         // why the read loop exited, set before done is closed
	closeOnce sync.Once
	closeErr  error

	mu             sync.RWMutex
	info           ServerInfo
	channels       map[uint64]*Channel
	services       map[uint32]*Service
	subs           map[uint32]uint64 // subscription ID → channel ID
	subsByChannel  map[uint64]uint32
	clientChannels map[uint32]bool
	nextSubID      uint32
	nextClientChID uint32
	nextCallID     uint32
	nextRequestID  uint32
	pendingCalls   map[uint32]chan callResult
	pendingParams  map[string]chan []Parameter
	pendingAssets  map[uint32]chan fetchAssetResponse
}

type callResult struct {
	resp ServiceCallResponse
	err  error
}

// Dial connects to the server at url (e.g. "ws://localhost:8765") and waits
// for its ServerInfo. ctx bounds the handshake only; the connection lives
// until Close is called or the server goes away.
func Dial(ctx context.Context, url string, opts *DialOptions) (*Client, error) {
	if opts == nil {
		opts = &DialOptions{}
	}

	subprotocols := opts.Subprotocols
	if len(subprotocols) == 0 {
		subprotocols = []string{SubprotocolSDK, SubprotocolLegacy}
	}

	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{
		HTTPClient:   opts.HTTPClient,
		HTTPHeader:   opts.HTTPHeader,
		Subprotocols: subprotocols,
	})
	if err != nil {
		return nil, fmt.Errorf("foxglove: dial %s: %w", url, err)
	}

	if conn.Subprotocol() == "" {
		conn.Close(websocket.StatusProtocolError, "no supported subprotocol")
		return nil, fmt.Errorf("foxglove: dial %s: server accepted none of the subprotocols %v", url, subprotocols)
	}

	if opts.ReadLimit > 0 {
		conn.SetReadLimit(opts.ReadLimit)
	} else {
		conn.SetReadLimit(-1)
	}

	// The server always opens with ServerInfo, so the client never exists
	// without knowing the server's capabilities.
	info, err := readServerInfo(ctx, conn)
	if err != nil {
		conn.CloseNow()
		return nil, fmt.Errorf("foxglove: dial %s: %w", url, err)
	}

	eventBuffer := opts.EventBuffer
	if eventBuffer <= 0 {
		eventBuffer = 1024
	}

	c := &Client{
		conn:           conn,
		subprotocol:    conn.Subprotocol(),
		events:         make(chan Event, eventBuffer),
		closing:        make(chan struct{}),
		done:           make(chan struct{}),
		info:           info,
		channels:       make(map[uint64]*Channel),
		services:       make(map[uint32]*Service),
		subs:           make(map[uint32]uint64),
		subsByChannel:  make(map[uint64]uint32),
		clientChannels: make(map[uint32]bool),
		pendingCalls:   make(map[uint32]chan callResult),
		pendingParams:  make(map[string]chan []Parameter),
		pendingAssets:  make(map[uint32]chan fetchAssetResponse),
	}
	c.ctx, c.cancel = context.WithCancel(context.Background())

	c.events <- info
	go c.readLoop()

	return c, nil
}

func readServerInfo(ctx context.Context, conn *websocket.Conn) (ServerInfo, error) {
	typ, data, err := conn.Read(ctx)
	if err != nil {
		return ServerInfo{}, fmt.Errorf("waiting for server info: %w", err)
	}
	if typ != websocket.MessageText {
		return ServerInfo{}, errors.New("expected server info, got a binary message")
	}
	ev, err := parseServerJSON(data)
	if err != nil {
		return ServerInfo{}, fmt.Errorf("decoding server info: %w", err)
	}
	info, ok := ev.(ServerInfo)
	if !ok {
		return ServerInfo{}, fmt.Errorf("expected server info, got %T", ev)
	}
	return info, nil
}

// Events returns the channel on which server messages are delivered. It is
// closed when the connection ends, after which Err reports why.
func (c *Client) Events() <-chan Event {
	return c.events
}

// Done is closed when the connection ends.
func (c *Client) Done() <-chan struct{} {
	return c.done
}

// Err returns nil while the connection is up. Afterwards it returns ErrClosed
// if Close was called, or the error that ended the connection.
func (c *Client) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

// Close closes the connection and waits for the client to shut down. Closing a
// connection the server already ended is not an error.
func (c *Client) Close() error {
	c.closeOnce.Do(func() {
		close(c.closing)
		select {
		case <-c.done:
		default:
			c.closeErr = c.conn.Close(websocket.StatusNormalClosure, "")
		}
		c.cancel()
	})
	<-c.done
	return c.closeErr
}

// Subprotocol returns the subprotocol negotiated with the server.
func (c *Client) Subprotocol() string {
	return c.subprotocol
}

// ServerInfo returns the server's description of itself.
func (c *Client) ServerInfo() ServerInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.info
}

// Channels returns the currently advertised channels, sorted by topic.
func (c *Client) Channels() []*Channel {
	c.mu.RLock()
	defer c.mu.RUnlock()
	channels := make([]*Channel, 0, len(c.channels))
	for _, ch := range c.channels {
		channels = append(channels, ch)
	}
	slices.SortFunc(channels, func(a, b *Channel) int {
		return cmp.Or(cmp.Compare(a.Topic, b.Topic), cmp.Compare(a.ID, b.ID))
	})
	return channels
}

// Channel returns the advertised channel with the given ID.
func (c *Client) Channel(id uint64) (*Channel, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	ch, ok := c.channels[id]
	return ch, ok
}

// ChannelByTopic returns the advertised channel for a topic. When a topic is
// advertised with several schemas, the channel with the lowest ID wins.
func (c *Client) ChannelByTopic(topic string) (*Channel, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var found *Channel
	for _, ch := range c.channels {
		if ch.Topic == topic && (found == nil || ch.ID < found.ID) {
			found = ch
		}
	}
	return found, found != nil
}

// Services returns the currently advertised services, sorted by name.
func (c *Client) Services() []*Service {
	c.mu.RLock()
	defer c.mu.RUnlock()
	services := make([]*Service, 0, len(c.services))
	for _, svc := range c.services {
		services = append(services, svc)
	}
	slices.SortFunc(services, func(a, b *Service) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})
	return services
}

// Service returns the advertised service with the given ID.
func (c *Client) Service(id uint32) (*Service, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	svc, ok := c.services[id]
	return svc, ok
}

// ServiceByName returns the advertised service with the given name.
func (c *Client) ServiceByName(name string) (*Service, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, svc := range c.services {
		if svc.Name == name {
			return svc, true
		}
	}
	return nil, false
}

// Subscribe asks the server to stream a channel's messages, which then arrive
// as MessageData events. Subscribing to a channel twice returns the existing
// subscription.
//
// Subscriptions end when the channel is unadvertised. If the channel comes
// back, it has to be subscribed to again.
func (c *Client) Subscribe(channelID uint64) (uint32, error) {
	c.mu.Lock()
	if _, ok := c.channels[channelID]; !ok {
		c.mu.Unlock()
		return 0, fmt.Errorf("foxglove: subscribe: unknown channel %d", channelID)
	}
	if id, ok := c.subsByChannel[channelID]; ok {
		c.mu.Unlock()
		return id, nil
	}
	id := c.nextSubID
	c.nextSubID++
	c.subs[id] = channelID
	c.subsByChannel[channelID] = id
	c.mu.Unlock()

	err := c.writeJSON(subscribeMsg{
		Op:            "subscribe",
		Subscriptions: []subscription{{ID: id, ChannelID: channelID}},
	})
	if err != nil {
		c.mu.Lock()
		c.dropSubscription(id)
		c.mu.Unlock()
		return 0, err
	}
	return id, nil
}

// Unsubscribe ends a subscription. Unknown subscriptions are ignored.
func (c *Client) Unsubscribe(subscriptionID uint32) error {
	c.mu.Lock()
	_, ok := c.subs[subscriptionID]
	c.dropSubscription(subscriptionID)
	c.mu.Unlock()
	if !ok {
		return nil
	}

	return c.writeJSON(unsubscribeMsg{
		Op:              "unsubscribe",
		SubscriptionIDs: []uint32{subscriptionID},
	})
}

// dropSubscription forgets a subscription. Must be called with mu held.
func (c *Client) dropSubscription(id uint32) {
	if channelID, ok := c.subs[id]; ok {
		delete(c.subs, id)
		delete(c.subsByChannel, channelID)
	}
}

// CallService calls a service and waits for its response. An empty encoding
// defaults to the one the service declared for its requests. A failure
// reported by the server is returned as a *ServiceCallError.
func (c *Client) CallService(ctx context.Context, serviceID uint32, encoding string, payload []byte) (ServiceCallResponse, error) {
	if err := c.require(CapabilityServices); err != nil {
		return ServiceCallResponse{}, err
	}

	c.mu.Lock()
	svc, ok := c.services[serviceID]
	if !ok {
		c.mu.Unlock()
		return ServiceCallResponse{}, fmt.Errorf("foxglove: call service: unknown service %d", serviceID)
	}
	if encoding == "" && svc.Request != nil {
		encoding = svc.Request.Encoding
	}
	callID := c.nextCallID
	c.nextCallID++
	reply := make(chan callResult, 1)
	c.pendingCalls[callID] = reply
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pendingCalls, callID)
		c.mu.Unlock()
	}()

	err := c.write(websocket.MessageBinary, encodeServiceCallRequest(serviceID, callID, encoding, payload))
	if err != nil {
		return ServiceCallResponse{}, err
	}

	select {
	case r := <-reply:
		return r.resp, r.err
	case <-ctx.Done():
		return ServiceCallResponse{}, ctx.Err()
	case <-c.done:
		return ServiceCallResponse{}, c.err
	}
}

// GetParameters fetches parameters by name, or all of them when no names are
// given.
func (c *Client) GetParameters(ctx context.Context, names ...string) ([]Parameter, error) {
	if err := c.require(CapabilityParameters); err != nil {
		return nil, err
	}
	return c.parameterRequest(ctx, func(id string) any {
		return getParametersMsg{Op: "getParameters", ParameterNames: nonNil(names), ID: id}
	})
}

// SetParameters sets parameters and returns their resulting values. Use
// UnsetParameter to remove a parameter.
func (c *Client) SetParameters(ctx context.Context, params ...Parameter) ([]Parameter, error) {
	if err := c.require(CapabilityParameters); err != nil {
		return nil, err
	}
	return c.parameterRequest(ctx, func(id string) any {
		return setParametersMsg{Op: "setParameters", Parameters: nonNil(params), ID: id}
	})
}

func (c *Client) parameterRequest(ctx context.Context, msg func(id string) any) ([]Parameter, error) {
	c.mu.Lock()
	id := "params-" + strconv.FormatUint(uint64(c.nextRequestID), 10)
	c.nextRequestID++
	reply := make(chan []Parameter, 1)
	c.pendingParams[id] = reply
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pendingParams, id)
		c.mu.Unlock()
	}()

	if err := c.writeJSON(msg(id)); err != nil {
		return nil, err
	}

	select {
	case params := <-reply:
		return params, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, c.err
	}
}

// SubscribeParameterUpdates asks the server to send ParameterValues events
// when the named parameters change, or any parameter when no names are given.
func (c *Client) SubscribeParameterUpdates(names ...string) error {
	if err := c.require(CapabilityParametersSubscribe); err != nil {
		return err
	}
	return c.writeJSON(parameterNamesMsg{Op: "subscribeParameterUpdates", ParameterNames: nonNil(names)})
}

// UnsubscribeParameterUpdates stops updates for the named parameters, or for
// all of them when no names are given.
func (c *Client) UnsubscribeParameterUpdates(names ...string) error {
	if err := c.require(CapabilityParametersSubscribe); err != nil {
		return err
	}
	return c.writeJSON(parameterNamesMsg{Op: "unsubscribeParameterUpdates", ParameterNames: nonNil(names)})
}

// SubscribeConnectionGraph asks the server to send ConnectionGraphUpdate
// events.
func (c *Client) SubscribeConnectionGraph() error {
	if err := c.require(CapabilityConnectionGraph); err != nil {
		return err
	}
	return c.writeJSON(opMsg{Op: "subscribeConnectionGraph"})
}

// UnsubscribeConnectionGraph stops ConnectionGraphUpdate events.
func (c *Client) UnsubscribeConnectionGraph() error {
	if err := c.require(CapabilityConnectionGraph); err != nil {
		return err
	}
	return c.writeJSON(opMsg{Op: "unsubscribeConnectionGraph"})
}

// FetchAsset fetches a file from the server, e.g. "package://my_robot/urdf/robot.urdf".
// A failure reported by the server is returned as an *AssetError.
func (c *Client) FetchAsset(ctx context.Context, uri string) ([]byte, error) {
	if err := c.require(CapabilityAssets); err != nil {
		return nil, err
	}

	c.mu.Lock()
	id := c.nextRequestID
	c.nextRequestID++
	reply := make(chan fetchAssetResponse, 1)
	c.pendingAssets[id] = reply
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pendingAssets, id)
		c.mu.Unlock()
	}()

	if err := c.writeJSON(fetchAssetMsg{Op: "fetchAsset", URI: uri, RequestID: id}); err != nil {
		return nil, err
	}

	select {
	case resp := <-reply:
		if resp.Failed {
			return nil, &AssetError{URI: uri, Message: resp.Error}
		}
		return resp.Data, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, c.err
	}
}

// Advertise announces a channel the client will publish on, and returns its
// ID for use with Publish.
func (c *Client) Advertise(ch ClientChannel) (uint32, error) {
	if err := c.require(CapabilityClientPublish); err != nil {
		return 0, err
	}

	c.mu.Lock()
	id := c.nextClientChID
	c.nextClientChID++
	c.clientChannels[id] = true
	c.mu.Unlock()

	err := c.writeJSON(clientAdvertiseMsg{
		Op: "advertise",
		Channels: []clientChannel{{
			ID:             id,
			Topic:          ch.Topic,
			Encoding:       ch.Encoding,
			SchemaName:     ch.SchemaName,
			SchemaEncoding: ch.SchemaEncoding,
			Schema:         encodeSchema(ch.SchemaEncoding, ch.Schema),
		}},
	})
	if err != nil {
		c.mu.Lock()
		delete(c.clientChannels, id)
		c.mu.Unlock()
		return 0, err
	}
	return id, nil
}

// Unadvertise withdraws a channel announced with Advertise. Unknown channels
// are ignored.
func (c *Client) Unadvertise(channelID uint32) error {
	c.mu.Lock()
	ok := c.clientChannels[channelID]
	delete(c.clientChannels, channelID)
	c.mu.Unlock()
	if !ok {
		return nil
	}
	return c.writeJSON(clientUnadvertiseMsg{Op: "unadvertise", ChannelIDs: []uint32{channelID}})
}

// Publish sends a message on a channel announced with Advertise. The payload
// must be in the channel's encoding.
func (c *Client) Publish(channelID uint32, payload []byte) error {
	c.mu.RLock()
	ok := c.clientChannels[channelID]
	c.mu.RUnlock()
	if !ok {
		return fmt.Errorf("foxglove: publish: channel %d is not advertised", channelID)
	}
	return c.write(websocket.MessageBinary, encodeClientMessageData(channelID, payload))
}

func (c *Client) require(capability Capability) error {
	if !c.ServerInfo().Has(capability) {
		return fmt.Errorf("%w: missing capability %q", ErrUnsupported, capability)
	}
	return nil
}

func (c *Client) writeJSON(msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("foxglove: encoding message: %w", err)
	}
	return c.write(websocket.MessageText, data)
}

func (c *Client) write(typ websocket.MessageType, data []byte) error {
	// Writes are bound to the client's lifetime rather than to a caller's
	// context: coder/websocket closes the connection when a write is
	// interrupted, and one impatient caller should not take it down.
	if err := c.conn.Write(c.ctx, typ, data); err != nil {
		select {
		case <-c.done:
			return c.err
		case <-c.closing:
			return ErrClosed
		default:
			return fmt.Errorf("foxglove: write: %w", err)
		}
	}
	return nil
}

func (c *Client) readLoop() {
	var err error
	for {
		var typ websocket.MessageType
		var data []byte
		typ, data, err = c.conn.Read(c.ctx)
		if err != nil {
			break
		}

		var ev Event
		var parseErr error
		switch typ {
		case websocket.MessageText:
			ev, parseErr = parseServerJSON(data)
		case websocket.MessageBinary:
			ev, parseErr = parseServerBinary(data)
		}
		if parseErr != nil {
			ev = ProtocolError{Err: parseErr}
		}

		if ev = c.handle(ev); ev != nil {
			c.emit(ev)
		}
	}

	select {
	case <-c.closing:
		c.err = ErrClosed
	default:
		c.err = fmt.Errorf("foxglove: connection lost: %w", err)
		c.conn.CloseNow()
		c.cancel()
	}
	close(c.events)
	close(c.done)
}

// emit delivers an event, blocking until the consumer takes it. Once Close
// has been called, events are dropped instead so the close handshake can
// complete.
func (c *Client) emit(ev Event) {
	select {
	case c.events <- ev:
	case <-c.closing:
	}
}

// handle updates the client's state from a server message. It returns the
// event to deliver, or nil when the message was consumed (a reply to a
// pending request) or should be dropped.
func (c *Client) handle(ev Event) Event {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch ev := ev.(type) {
	case ServerInfo:
		c.info = ev

	case Advertise:
		for _, ch := range ev.Channels {
			c.channels[ch.ID] = &ch
		}

	case Unadvertise:
		for _, id := range ev.ChannelIDs {
			delete(c.channels, id)
			if subID, ok := c.subsByChannel[id]; ok {
				c.dropSubscription(subID)
			}
		}

	case AdvertiseServices:
		for _, svc := range ev.Services {
			c.services[svc.ID] = &svc
		}

	case UnadvertiseServices:
		for _, id := range ev.ServiceIDs {
			delete(c.services, id)
		}

	case MessageData:
		// Messages can still be in flight after an unsubscribe.
		channelID, ok := c.subs[ev.SubscriptionID]
		if !ok {
			return nil
		}
		ev.Channel = c.channels[channelID]
		if ev.Channel == nil {
			return nil
		}
		return ev

	case ParameterValues:
		if reply, ok := c.pendingParams[ev.ID]; ok && ev.ID != "" {
			delete(c.pendingParams, ev.ID)
			reply <- ev.Parameters
			return nil
		}

	case serviceCallResponse:
		if reply, ok := c.pendingCalls[ev.CallID]; ok {
			delete(c.pendingCalls, ev.CallID)
			reply <- callResult{resp: ev.ServiceCallResponse}
		}
		return nil

	case serviceCallFailure:
		if reply, ok := c.pendingCalls[ev.CallID]; ok {
			delete(c.pendingCalls, ev.CallID)
			reply <- callResult{err: &ServiceCallError{ServiceID: ev.ServiceID, CallID: ev.CallID, Message: ev.Message}}
		}
		return nil

	case fetchAssetResponse:
		if reply, ok := c.pendingAssets[ev.RequestID]; ok {
			delete(c.pendingAssets, ev.RequestID)
			reply <- ev
		}
		return nil
	}

	return ev
}

// nonNil keeps empty lists from being encoded as null, since an empty list
// means "all" to the server.
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
