package foxglove

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
)

// Binary opcodes sent by the server.
const (
	opMessageData         = 0x01
	opTime                = 0x02
	opServiceCallResponse = 0x03
	opFetchAssetResponse  = 0x04
)

// Binary opcodes sent by the client.
const (
	opClientMessageData  = 0x01
	opServiceCallRequest = 0x02
)

var errShortMessage = errors.New("binary message too short")

// parseServerJSON decodes a text message from the server. Unknown operations
// yield a nil event and no error, so that newer servers keep working.
func parseServerJSON(data []byte) (Event, error) {
	var head struct {
		Op string `json:"op"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, err
	}

	switch head.Op {
	case "serverInfo":
		return decodeJSON[ServerInfo](data)
	case "status":
		return decodeJSON[Status](data)
	case "removeStatus":
		return decodeJSON[RemoveStatus](data)
	case "advertise":
		return decodeJSON[Advertise](data)
	case "unadvertise":
		return decodeJSON[Unadvertise](data)
	case "advertiseServices":
		return decodeJSON[AdvertiseServices](data)
	case "unadvertiseServices":
		return decodeJSON[UnadvertiseServices](data)
	case "parameterValues":
		return decodeJSON[ParameterValues](data)
	case "connectionGraphUpdate":
		return decodeJSON[ConnectionGraphUpdate](data)
	case "serviceCallFailure":
		return decodeJSON[serviceCallFailure](data)
	default:
		return nil, nil
	}
}

func decodeJSON[T Event](data []byte) (Event, error) {
	var msg T
	// Parameter values are decoded into `any`, and UseNumber keeps integers
	// apart from floats there. It has no effect on typed fields.
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&msg); err != nil {
		return nil, err
	}
	return msg, nil
}

// parseServerBinary decodes a binary message from the server. Unknown opcodes
// yield a nil event and no error. The returned event may alias data.
func parseServerBinary(data []byte) (Event, error) {
	if len(data) == 0 {
		return nil, errors.New("empty binary message")
	}

	op, data := data[0], data[1:]
	le := binary.LittleEndian

	switch op {
	case opMessageData:
		if len(data) < 4+8 {
			return nil, fmt.Errorf("message data: %w", errShortMessage)
		}
		return MessageData{
			SubscriptionID: le.Uint32(data[0:4]),
			Timestamp:      le.Uint64(data[4:12]),
			Data:           data[12:],
		}, nil

	case opTime:
		if len(data) < 8 {
			return nil, fmt.Errorf("time: %w", errShortMessage)
		}
		return Time{Timestamp: le.Uint64(data)}, nil

	case opServiceCallResponse:
		if len(data) < 4+4+4 {
			return nil, fmt.Errorf("service call response: %w", errShortMessage)
		}
		encodingLen := int(le.Uint32(data[8:12]))
		if len(data)-12 < encodingLen {
			return nil, fmt.Errorf("service call response: %w", errShortMessage)
		}
		return serviceCallResponse{ServiceCallResponse{
			ServiceID: le.Uint32(data[0:4]),
			CallID:    le.Uint32(data[4:8]),
			Encoding:  string(data[12 : 12+encodingLen]),
			Payload:   data[12+encodingLen:],
		}}, nil

	case opFetchAssetResponse:
		if len(data) < 4+1+4 {
			return nil, fmt.Errorf("fetch asset response: %w", errShortMessage)
		}
		status := data[4]
		errorLen := int(le.Uint32(data[5:9]))
		if len(data)-9 < errorLen {
			return nil, fmt.Errorf("fetch asset response: %w", errShortMessage)
		}
		resp := fetchAssetResponse{RequestID: le.Uint32(data[0:4])}
		switch status {
		case 0:
			resp.Data = data[9+errorLen:]
		case 1:
			resp.Failed = true
			resp.Error = string(data[9 : 9+errorLen])
		default:
			return nil, fmt.Errorf("fetch asset response: invalid status %d", status)
		}
		return resp, nil

	default:
		return nil, nil
	}
}

func encodeClientMessageData(channelID uint32, payload []byte) []byte {
	buf := make([]byte, 0, 1+4+len(payload))
	buf = append(buf, opClientMessageData)
	buf = binary.LittleEndian.AppendUint32(buf, channelID)
	return append(buf, payload...)
}

func encodeServiceCallRequest(serviceID, callID uint32, encoding string, payload []byte) []byte {
	buf := make([]byte, 0, 1+4+4+4+len(encoding)+len(payload))
	buf = append(buf, opServiceCallRequest)
	buf = binary.LittleEndian.AppendUint32(buf, serviceID)
	buf = binary.LittleEndian.AppendUint32(buf, callID)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(encoding)))
	buf = append(buf, encoding...)
	return append(buf, payload...)
}

// Text messages sent by the client.

type subscription struct {
	ID        uint32 `json:"id"`
	ChannelID uint64 `json:"channelId"`
}

type subscribeMsg struct {
	Op            string         `json:"op"`
	Subscriptions []subscription `json:"subscriptions"`
}

type unsubscribeMsg struct {
	Op              string   `json:"op"`
	SubscriptionIDs []uint32 `json:"subscriptionIds"`
}

type clientChannel struct {
	ID             uint32 `json:"id"`
	Topic          string `json:"topic"`
	Encoding       string `json:"encoding"`
	SchemaName     string `json:"schemaName"`
	SchemaEncoding string `json:"schemaEncoding,omitempty"`
	Schema         string `json:"schema,omitempty"`
}

type clientAdvertiseMsg struct {
	Op       string          `json:"op"`
	Channels []clientChannel `json:"channels"`
}

type clientUnadvertiseMsg struct {
	Op         string   `json:"op"`
	ChannelIDs []uint32 `json:"channelIds"`
}

type getParametersMsg struct {
	Op             string   `json:"op"`
	ParameterNames []string `json:"parameterNames"`
	ID             string   `json:"id,omitempty"`
}

type setParametersMsg struct {
	Op         string      `json:"op"`
	Parameters []Parameter `json:"parameters"`
	ID         string      `json:"id,omitempty"`
}

type parameterNamesMsg struct {
	Op             string   `json:"op"`
	ParameterNames []string `json:"parameterNames"`
}

type opMsg struct {
	Op string `json:"op"`
}

type fetchAssetMsg struct {
	Op        string `json:"op"`
	URI       string `json:"uri"`
	RequestID uint32 `json:"requestId"`
}
