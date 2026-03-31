package link

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
)

// ProtocolID is the libp2p protocol identifier for skylink request/response.
const ProtocolID = protocol.ID("/skylink/1.0.0")

// MaxMessageSize is the maximum size of a single message frame (4MB).
const MaxMessageSize = 4 * 1024 * 1024

// Message is the wire format for skylink communication.
type Message struct {
	ID     string          `json:"id"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *MessageError   `json:"error,omitempty"`
}

// MessageError is an error in a skylink response.
type MessageError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *MessageError) Error() string { return e.Message }

// WriteMessage writes a length-prefixed JSON message to w.
func WriteMessage(w io.Writer, msg *Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshaling message: %w", err)
	}
	if len(data) > MaxMessageSize {
		return fmt.Errorf("message too large: %d > %d", len(data), MaxMessageSize)
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return fmt.Errorf("writing length: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("writing payload: %w", err)
	}
	return nil
}

// ReadMessage reads a length-prefixed JSON message from r.
func ReadMessage(r io.Reader) (*Message, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, fmt.Errorf("reading length: %w", err)
	}
	size := binary.BigEndian.Uint32(lenBuf[:])
	if size > MaxMessageSize {
		return nil, fmt.Errorf("message too large: %d > %d", size, MaxMessageSize)
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("reading payload: %w", err)
	}
	var msg Message
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("unmarshaling message: %w", err)
	}
	return &msg, nil
}

// Call opens a stream to the target peer, sends a request, and reads
// the response. The stream is closed after the call completes.
func (n *Node) Call(ctx context.Context, target peer.ID, method string, params interface{}) (json.RawMessage, error) {
	if n.host == nil {
		return nil, fmt.Errorf("node not running")
	}

	s, err := n.host.NewStream(ctx, target, ProtocolID)
	if err != nil {
		return nil, fmt.Errorf("opening stream to %s: %w", target, err)
	}
	defer s.Close()

	// Propagate context deadline to stream so reads/writes respect it.
	if deadline, ok := ctx.Deadline(); ok {
		s.SetDeadline(deadline)
	}

	// Marshal params.
	var rawParams json.RawMessage
	if params != nil {
		rawParams, err = json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshaling params: %w", err)
		}
	}

	// Send request.
	req := &Message{
		ID:     uuid.NewString(),
		Method: method,
		Params: rawParams,
	}
	if err := WriteMessage(s, req); err != nil {
		return nil, fmt.Errorf("writing request: %w", err)
	}

	// Half-close: signal we're done sending.
	if err := s.CloseWrite(); err != nil {
		return nil, fmt.Errorf("closing write: %w", err)
	}

	// Read response.
	resp, err := ReadMessage(s)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp.Result, nil
}
