package commands

import (
	"encoding/json"
	"fmt"
	"net"

	skyfs "github.com/sky10/sky10/pkg/fs"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
)

// dialDaemon connects to the running daemon's Unix socket.
func dialDaemon() (net.Conn, error) {
	sockPath := skyfs.DaemonSocketPath()
	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		return nil, fmt.Errorf("daemon not running (start with 'sky10 fs serve' or Cirrus): %w", err)
	}
	return conn, nil
}

// rpcCall sends a JSON-RPC 2.0 request to the daemon and returns the result.
func rpcCall(method string, params interface{}) (json.RawMessage, error) {
	conn, err := dialDaemon()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	var rawParams json.RawMessage
	if params != nil {
		rawParams, _ = json.Marshal(params)
	}

	req := skyrpc.Request{
		JSONRPC: "2.0",
		Method:  method,
		Params:  rawParams,
		ID:      1,
	}

	encoder := json.NewEncoder(conn)
	if err := encoder.Encode(req); err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}

	var resp struct {
		Result json.RawMessage `json:"result,omitempty"`
		Error  *skyrpc.Error   `json:"error,omitempty"`
	}
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s", resp.Error.Message)
	}
	return resp.Result, nil
}
