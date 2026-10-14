package foxglove

import (
	"encoding/base64"
	"slices"
)

// WebSocket subprotocols understood by this client.
const (
	// SubprotocolSDK is negotiated by servers built on the Foxglove SDK,
	// including current releases of foxglove_bridge.
	SubprotocolSDK = "foxglove.sdk.v1"
	// SubprotocolLegacy is negotiated by servers built on the deprecated
	// ws-protocol libraries.
	SubprotocolLegacy = "foxglove.websocket.v1"
)

// Capability is an optional feature a server advertises in ServerInfo.
type Capability string

// Capabilities defined by the protocol.
const (
	CapabilityClientPublish       Capability = "clientPublish"
	CapabilityParameters          Capability = "parameters"
	CapabilityParametersSubscribe Capability = "parametersSubscribe"
	CapabilityTime                Capability = "time"
	CapabilityServices            Capability = "services"
	CapabilityConnectionGraph     Capability = "connectionGraph"
	CapabilityAssets              Capability = "assets"
	CapabilityPlaybackControl     Capability = "playbackControl"
)

// Event is a message received from the server. The concrete types are the
// server message structs in this package (ServerInfo, Advertise, MessageData,
// ...), always delivered by value.
type Event interface {
	isEvent()
}

// Timestamp is a point in time split into seconds and nanoseconds, as used by
// ServerInfo to describe the range of recorded data.
type Timestamp struct {
	Sec  uint32 `json:"sec"`
	Nsec uint32 `json:"nsec"`
}

// Nanoseconds returns the timestamp as nanoseconds since the epoch.
func (t Timestamp) Nanoseconds() uint64 {
	return uint64(t.Sec)*1e9 + uint64(t.Nsec)
}

// ServerInfo is sent by the server as the first message of every connection.
type ServerInfo struct {
	Name         string       `json:"name"`
	Capabilities []Capability `json:"capabilities"`
	// SupportedEncodings lists the encodings that may be used for client
	// publishing and service calls. Only set when either is supported.
	SupportedEncodings []string          `json:"supportedEncodings,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	// SessionID changes when the server restarts, which lets a client tell a
	// reconnection apart from a connection to a new server instance.
	SessionID     string     `json:"sessionId,omitempty"`
	DataStartTime *Timestamp `json:"dataStartTime,omitempty"`
	DataEndTime   *Timestamp `json:"dataEndTime,omitempty"`
}

// Has reports whether the server advertised the capability.
func (s ServerInfo) Has(c Capability) bool {
	return slices.Contains(s.Capabilities, c)
}

// StatusLevel is the severity of a Status message.
type StatusLevel uint8

// Status levels defined by the protocol.
const (
	StatusInfo    StatusLevel = 0
	StatusWarning StatusLevel = 1
	StatusError   StatusLevel = 2
)

func (l StatusLevel) String() string {
	switch l {
	case StatusInfo:
		return "info"
	case StatusWarning:
		return "warning"
	case StatusError:
		return "error"
	default:
		return "unknown"
	}
}

// Status is a diagnostic message the server may send at any time.
type Status struct {
	Level   StatusLevel `json:"level"`
	Message string      `json:"message"`
	// ID, when set, identifies the status so that a later Status with the same
	// ID replaces it, or a RemoveStatus clears it.
	ID string `json:"id,omitempty"`
}

// RemoveStatus informs the client that previously sent statuses are no longer
// valid.
type RemoveStatus struct {
	StatusIDs []string `json:"statusIds"`
}

// Channel is a stream of messages advertised by the server, usually one per
// topic.
//
// Channels handed out by the client are shared and must not be modified.
type Channel struct {
	ID       uint64 `json:"id"`
	Topic    string `json:"topic"`
	Encoding string `json:"encoding"`
	// SchemaName is the message type, e.g. "geometry_msgs/msg/Twist".
	SchemaName string `json:"schemaName"`
	// SchemaEncoding is the format of Schema, e.g. "ros2msg" or "ros2idl".
	SchemaEncoding string `json:"schemaEncoding,omitempty"`
	// Schema is the protocol encoded schema. Use SchemaData to decode it.
	Schema   string            `json:"schema"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

// SchemaData returns the decoded schema. Text schemas (ros2msg, ros2idl,
// jsonschema, ...) are returned as is, binary ones (protobuf, flatbuffer) are
// base64 decoded.
func (c Channel) SchemaData() ([]byte, error) {
	encoding := c.SchemaEncoding
	if encoding == "" {
		// The protocol lets servers omit the schema encoding when it can be
		// deduced from the message encoding.
		encoding = c.Encoding
	}
	return decodeSchema(encoding, c.Schema)
}

// Advertise informs the client about new channels.
type Advertise struct {
	Channels []Channel `json:"channels"`
}

// Unadvertise informs the client that channels are gone. Subscriptions to
// those channels are dropped.
type Unadvertise struct {
	ChannelIDs []uint64 `json:"channelIds"`
}

// MessageData carries one message from a subscribed channel.
type MessageData struct {
	SubscriptionID uint32
	// Timestamp is the log or receive time of the message, in nanoseconds.
	Timestamp uint64
	// Channel describes how Data is encoded. It is shared and must not be
	// modified.
	Channel *Channel
	Data    []byte
}

// Time is the latest server time, in nanoseconds. Only sent by servers with
// the time capability, in which case MessageData timestamps use the same
// clock.
type Time struct {
	Timestamp uint64
}

// MessageSchema describes the encoding of a service request or response.
type MessageSchema struct {
	Encoding       string `json:"encoding"`
	SchemaName     string `json:"schemaName"`
	SchemaEncoding string `json:"schemaEncoding"`
	Schema         string `json:"schema"`
}

// SchemaData returns the decoded schema, see Channel.SchemaData.
func (m MessageSchema) SchemaData() ([]byte, error) {
	return decodeSchema(m.SchemaEncoding, m.Schema)
}

// Service is a service advertised by the server.
//
// Services handed out by the client are shared and must not be modified.
type Service struct {
	ID   uint32 `json:"id"`
	Name string `json:"name"`
	// Type is the service type, e.g. "std_srvs/srv/SetBool".
	Type     string         `json:"type"`
	Request  *MessageSchema `json:"request,omitempty"`
	Response *MessageSchema `json:"response,omitempty"`
	// RequestSchema and ResponseSchema are the legacy way of describing
	// requests and responses, used when Request and Response are not set. Their
	// encoding is one of ServerInfo.SupportedEncodings.
	RequestSchema  string `json:"requestSchema,omitempty"`
	ResponseSchema string `json:"responseSchema,omitempty"`
}

// AdvertiseServices informs the client about new services.
type AdvertiseServices struct {
	Services []Service `json:"services"`
}

// UnadvertiseServices informs the client that services are gone.
type UnadvertiseServices struct {
	ServiceIDs []uint32 `json:"serviceIds"`
}

// ParameterValues carries parameters the client subscribed to with
// SubscribeParameterUpdates. Replies to GetParameters and SetParameters are
// returned by those methods instead.
type ParameterValues struct {
	Parameters []Parameter `json:"parameters"`
	ID         string      `json:"id,omitempty"`
}

// PublishedTopic is a topic and the nodes publishing it.
type PublishedTopic struct {
	Name         string   `json:"name"`
	PublisherIDs []string `json:"publisherIds"`
}

// SubscribedTopic is a topic and the nodes subscribed to it.
type SubscribedTopic struct {
	Name          string   `json:"name"`
	SubscriberIDs []string `json:"subscriberIds"`
}

// AdvertisedService is a service and the nodes providing it.
type AdvertisedService struct {
	Name        string   `json:"name"`
	ProviderIDs []string `json:"providerIds"`
}

// ConnectionGraphUpdate describes changes to the ROS graph. Entries replace
// whatever was previously known about the same topic or service. Only sent
// after SubscribeConnectionGraph.
type ConnectionGraphUpdate struct {
	PublishedTopics    []PublishedTopic    `json:"publishedTopics"`
	SubscribedTopics   []SubscribedTopic   `json:"subscribedTopics"`
	AdvertisedServices []AdvertisedService `json:"advertisedServices"`
	RemovedTopics      []string            `json:"removedTopics"`
	RemovedServices    []string            `json:"removedServices"`
}

// ServiceCallResponse is the reply to a successful service call, returned by
// Client.CallService.
type ServiceCallResponse struct {
	ServiceID uint32
	CallID    uint32
	Encoding  string
	Payload   []byte
}

// ProtocolError reports a message from the server that could not be decoded.
// The message is skipped and the connection stays open.
type ProtocolError struct {
	Err error
}

// serviceCallResponse wraps a ServiceCallResponse on its way from the parser
// to CallService. Never delivered as an event.
type serviceCallResponse struct {
	ServiceCallResponse
}

// serviceCallFailure is the reply to a failed service call. It is never
// delivered as an event: CallService turns it into a *ServiceCallError.
type serviceCallFailure struct {
	ServiceID uint32 `json:"serviceId"`
	CallID    uint32 `json:"callId"`
	Message   string `json:"message"`
}

// fetchAssetResponse is the reply to FetchAsset. Never delivered as an event.
type fetchAssetResponse struct {
	RequestID uint32
	Error     string
	Data      []byte
	Failed    bool
}

func (ServerInfo) isEvent()            {}
func (Status) isEvent()                {}
func (RemoveStatus) isEvent()          {}
func (Advertise) isEvent()             {}
func (Unadvertise) isEvent()           {}
func (MessageData) isEvent()           {}
func (Time) isEvent()                  {}
func (AdvertiseServices) isEvent()     {}
func (UnadvertiseServices) isEvent()   {}
func (ParameterValues) isEvent()       {}
func (ConnectionGraphUpdate) isEvent() {}
func (ProtocolError) isEvent()         {}
func (serviceCallResponse) isEvent()   {}
func (serviceCallFailure) isEvent()    {}
func (fetchAssetResponse) isEvent()    {}

// ClientChannel describes a channel the client publishes on, see
// Client.Advertise.
type ClientChannel struct {
	Topic string
	// Encoding of the published messages. Must be one of
	// ServerInfo.SupportedEncodings, e.g. "cdr" or "json".
	Encoding       string
	SchemaName     string
	SchemaEncoding string
	// Schema is the raw schema. It is base64 encoded on the wire for binary
	// schema encodings (protobuf, flatbuffer).
	Schema []byte
}

// isBinarySchemaEncoding reports whether schemas in this encoding are binary,
// and so travel base64 encoded inside JSON messages.
func isBinarySchemaEncoding(encoding string) bool {
	return encoding == "protobuf" || encoding == "flatbuffer"
}

func decodeSchema(encoding, schema string) ([]byte, error) {
	if isBinarySchemaEncoding(encoding) {
		return base64.StdEncoding.DecodeString(schema)
	}
	return []byte(schema), nil
}

func encodeSchema(encoding string, schema []byte) string {
	if isBinarySchemaEncoding(encoding) {
		return base64.StdEncoding.EncodeToString(schema)
	}
	return string(schema)
}
