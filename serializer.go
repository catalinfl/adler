package adler

import (
	"encoding/json"

	"github.com/gobwas/ws"
	"google.golang.org/protobuf/proto"
)

// Serializer defines the interface for marshaling and unmarshaling messages
// in different formats (JSON, Protobuf, etc.) for WebSocket transmission.
type Serializer interface {
	// Marshal converts a value to bytes and returns the appropriate WebSocket opcode.
	Marshal(v any) ([]byte, ws.OpCode, error)
	// Unmarshal decodes bytes into a value.
	Unmarshal(data []byte, v any) error
}

// JSONSerializer marshals messages as JSON text messages and unmarshals from JSON.
type JSONSerializer struct{}

// ProtobufSerializer marshals messages as binary protobuf messages and unmarshals from protobuf.
type ProtobufSerializer struct{}

// Marshal encodes v as JSON and returns it with text opcode.
func (JSONSerializer) Marshal(v any) ([]byte, ws.OpCode, error) {
	data, err := json.Marshal(v)
	return data, ws.OpText, err
}

// Unmarshal decodes JSON data into v.
func (JSONSerializer) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}

// Marshal encodes v as binary protobuf and returns it with binary opcode.
// Returns ErrProtobufValue if v does not implement proto.Message.
func (ProtobufSerializer) Marshal(v any) ([]byte, ws.OpCode, error) {
	msg, ok := v.(proto.Message)
	if !ok {
		return nil, 0, ErrProtobufValue
	}

	data, err := proto.Marshal(msg)
	return data, ws.OpBinary, err
}

// Unmarshal decodes binary protobuf data into v.
// Returns ErrProtobufTarget if v does not implement proto.Message.
func (ProtobufSerializer) Unmarshal(data []byte, v any) error {
	msg, ok := v.(proto.Message)
	if !ok {
		return ErrProtobufTarget
	}

	return proto.Unmarshal(data, msg)
}
