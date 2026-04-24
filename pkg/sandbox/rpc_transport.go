package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	skyfs "github.com/sky10/sky10/pkg/fs"
	skyrpc "github.com/sky10/sky10/pkg/rpc"
)

func findLocalTemplateFile(name string) (string, error) {
	candidates := make([]string, 0, 2)
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, cwd)
	}
	if exe, err := os.Executable(); err == nil {
		candidates = append(candidates, filepath.Dir(exe))
	}
	for _, start := range candidates {
		for _, dir := range walkUp(start) {
			path := filepath.Join(dir, "templates", "lima", name)
			if info, err := os.Stat(path); err == nil && !info.IsDir() {
				return path, nil
			}
		}
	}
	return "", fmt.Errorf("template %q not found locally", name)
}

func loadSandboxAsset(ctx context.Context, name string) ([]byte, error) {
	if local, err := findLocalTemplateFile(name); err == nil {
		data, err := os.ReadFile(local)
		if err != nil {
			return nil, fmt.Errorf("reading local sandbox template asset: %w", err)
		}
		return data, nil
	}
	if data, err := readBundledTemplateAsset(name); err == nil {
		return data, nil
	}
	return httpRequest(ctx, templateRemoteBase+name)
}

func loadSandboxAssets(ctx context.Context, names []string) (map[string][]byte, error) {
	assets := make(map[string][]byte, len(names))
	for _, name := range names {
		body, err := loadSandboxAsset(ctx, name)
		if err != nil {
			return nil, err
		}
		assets[name] = body
	}
	return assets, nil
}

func walkUp(start string) []string {
	start = filepath.Clean(start)
	var dirs []string
	for {
		dirs = append(dirs, start)
		parent := filepath.Dir(start)
		if parent == start {
			break
		}
		start = parent
	}
	return dirs
}

func httpRequest(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("building request for %q: %w", url, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("downloading sandbox template %q: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading sandbox template %q: unexpected HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading sandbox template %q: %w", url, err)
	}
	return body, nil
}

func lookupLimaInstanceIPv4(ctx context.Context, outputCmd func(context.Context, string, []string) ([]byte, error), limactl, name string) (string, error) {
	commands := []string{
		`ip -4 route get 1.1.1.1 2>/dev/null | awk '{for (i = 1; i <= NF; i++) if ($i == "src") {print $(i + 1); exit}}'`,
		`ip -4 -o addr show scope global | awk '{split($4, a, "/"); if (a[1] !~ /^127\./) {print a[1]; exit}}'`,
		`ip -4 addr show dev lima0 2>/dev/null | awk '/inet / {sub(/\/.*/, "", $2); print $2; exit}'`,
	}
	var lastErr error
	for _, script := range commands {
		out, err := outputCmd(ctx, limactl, []string{"shell", name, "--", "bash", "-lc", script})
		if err != nil {
			lastErr = err
			continue
		}
		if ip := strings.TrimSpace(string(out)); ip != "" {
			return ip, nil
		}
	}
	if lastErr != nil {
		return "", fmt.Errorf("querying guest IP: %w", lastErr)
	}
	return "", nil
}

func guestSky10RPCAddress(rec Record) string {
	host := strings.TrimSpace(rec.ForwardedHost)
	if host != "" && rec.ForwardedPort > 0 {
		return "http://" + net.JoinHostPort(host, strconv.Itoa(rec.ForwardedPort))
	}
	return strings.TrimSpace(rec.IPAddress)
}

func guestRPCCall(ctx context.Context, address, method string, params interface{}, out interface{}) error {
	url := guestSky10RPCURL(address)
	var rawParams json.RawMessage
	if params != nil {
		body, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal guest rpc params for %s: %w", method, err)
		}
		rawParams = body
	}
	reqBody, err := json.Marshal(skyrpc.Request{
		JSONRPC: "2.0",
		Method:  method,
		Params:  rawParams,
		ID:      1,
	})
	if err != nil {
		return fmt.Errorf("marshal guest rpc request for %s: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("build guest rpc request for %s: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("post guest rpc %s: %w", method, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("guest rpc %s: unexpected HTTP %d: %s", method, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var rpcResp struct {
		Result json.RawMessage `json:"result,omitempty"`
		Error  *skyrpc.Error   `json:"error,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return fmt.Errorf("decode guest rpc %s response: %w", method, err)
	}
	if rpcResp.Error != nil {
		return fmt.Errorf("%s", rpcResp.Error.Message)
	}
	if out == nil || len(rpcResp.Result) == 0 || bytes.Equal(rpcResp.Result, []byte("null")) {
		return nil
	}
	if err := json.Unmarshal(rpcResp.Result, out); err != nil {
		return fmt.Errorf("decode guest rpc %s result: %w", method, err)
	}
	return nil
}

func hostRPCCall(ctx context.Context, method string, params interface{}, out interface{}) error {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", skyfs.DaemonSocketPath())
	if err != nil {
		return fmt.Errorf("dial host daemon for %s: %w", method, err)
	}
	defer conn.Close()

	var rawParams json.RawMessage
	if params != nil {
		body, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("marshal host rpc params for %s: %w", method, err)
		}
		rawParams = body
	}

	req := skyrpc.Request{
		JSONRPC: "2.0",
		Method:  method,
		Params:  rawParams,
		ID:      1,
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return fmt.Errorf("send host rpc %s: %w", method, err)
	}

	var rpcResp struct {
		Result json.RawMessage `json:"result,omitempty"`
		Error  *skyrpc.Error   `json:"error,omitempty"`
	}
	if err := json.NewDecoder(conn).Decode(&rpcResp); err != nil {
		return fmt.Errorf("decode host rpc %s response: %w", method, err)
	}
	if rpcResp.Error != nil {
		return fmt.Errorf("%s", rpcResp.Error.Message)
	}
	if out == nil || len(rpcResp.Result) == 0 || bytes.Equal(rpcResp.Result, []byte("null")) {
		return nil
	}
	if err := json.Unmarshal(rpcResp.Result, out); err != nil {
		return fmt.Errorf("decode host rpc %s result: %w", method, err)
	}
	return nil
}

func guestSky10RPCURL(address string) string {
	base := strings.TrimSpace(address)
	if strings.HasPrefix(base, "http://") || strings.HasPrefix(base, "https://") {
		return strings.TrimRight(base, "/") + "/rpc"
	}
	return fmt.Sprintf("http://%s:9101/rpc", base)
}

func defaultRunCommand(ctx context.Context, bin string, args []string, onLine func(stream, line string)) error {
	cmd := exec.CommandContext(ctx, bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("opening stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("opening stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var wg sync.WaitGroup
	readPipe := func(stream string, r io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			onLine(stream, scanner.Text())
		}
	}
	wg.Add(2)
	go readPipe("stdout", stdout)
	go readPipe("stderr", stderr)
	wg.Wait()
	return cmd.Wait()
}

func defaultOutputCommand(ctx context.Context, bin string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	return cmd.Output()
}
