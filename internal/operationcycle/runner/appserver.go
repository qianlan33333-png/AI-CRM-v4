package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// AppServerProxy talks only to Codex's managed local proxy. It never invokes
// codex exec and never derives a workspace path from an action.
type AppServerProxy struct {
	Binary          string
	SocketPath      string
	ExpectedVersion string
	Timeout         time.Duration
}

func (p AppServerProxy) Available(ctx context.Context) error {
	if err := p.preflight(ctx); err != nil {
		return err
	}
	return p.withProxy(ctx, func(client *jsonRPCClient, callCtx context.Context) error {
		_, err := p.initialize(callCtx, client)
		return err
	})
}
func (p AppServerProxy) preflight(ctx context.Context) error {
	if strings.TrimSpace(p.Binary) == "" || strings.TrimSpace(p.SocketPath) == "" || strings.TrimSpace(p.ExpectedVersion) == "" {
		return errors.New("Codex app-server configuration is incomplete")
	}
	if !filepath.IsAbs(p.Binary) {
		return errors.New("Codex binary must be an absolute path")
	}
	versionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	actual, err := exec.CommandContext(versionCtx, p.Binary, "--version").Output()
	if err != nil || strings.TrimSpace(string(actual)) != p.ExpectedVersion {
		return errors.New("Codex version is unavailable or incompatible")
	}
	info, err := os.Stat(p.SocketPath)
	if err != nil {
		return fmt.Errorf("Codex app-server socket unavailable: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return errors.New("Codex app-server path is not a socket")
	}
	probeCtx, cancel := context.WithTimeout(ctx, p.timeout())
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(probeCtx, "unix", p.SocketPath)
	if err != nil {
		return fmt.Errorf("Codex app-server socket is not connectable: %w", err)
	}
	_ = connection.Close()
	return nil
}
func (p AppServerProxy) timeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return 30 * time.Second
}
func (p AppServerProxy) StartThread(ctx context.Context, directory string) (string, error) {
	value, err := p.request(ctx, "thread/start", map[string]any{"cwd": directory, "approvalPolicy": "on-request", "sandbox": "workspace-write", "ephemeral": false})
	if err != nil {
		return "", err
	}
	thread, _ := value["thread"].(map[string]any)
	id := stringValue(thread, "id")
	if id == "" {
		id = stringValue(value, "id")
	}
	if id == "" {
		return "", errors.New("Codex app-server did not return a thread id")
	}
	return id, nil
}
func (p AppServerProxy) StartTurn(ctx context.Context, threadID, prompt string) (string, error) {
	value, err := p.request(ctx, "turn/start", map[string]any{"threadId": threadID, "input": []any{map[string]any{"type": "text", "text": prompt}}})
	if err != nil {
		return "", err
	}
	turn, _ := value["turn"].(map[string]any)
	id := stringValue(turn, "id")
	if id == "" {
		id = stringValue(value, "id")
	}
	if id == "" {
		return "", errors.New("Codex app-server did not return a turn id")
	}
	return id, nil
}
func (p AppServerProxy) request(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	if err := p.preflight(ctx); err != nil {
		return nil, err
	}
	var result map[string]any
	err := p.withProxy(ctx, func(client *jsonRPCClient, callCtx context.Context) error {
		if _, err := p.initialize(callCtx, client); err != nil {
			return err
		}
		var err error
		result, err = client.call(callCtx, method, params)
		return err
	})
	return result, err
}
func (p AppServerProxy) initialize(ctx context.Context, client *jsonRPCClient) (map[string]any, error) {
	result, err := client.call(ctx, "initialize", map[string]any{"clientInfo": map[string]any{"name": "aicrm-operation-cycle-runner", "title": "AI-CRM Operation Runner", "version": connectorVersion}, "capabilities": map[string]any{"experimentalApi": true}})
	if err != nil {
		return nil, err
	}
	if err = client.notify(ctx, "initialized", map[string]any{}); err != nil {
		return nil, err
	}
	return result, nil
}
func (p AppServerProxy) withProxy(ctx context.Context, callback func(*jsonRPCClient, context.Context) error) error {
	callCtx, cancel := context.WithTimeout(ctx, p.timeout())
	defer cancel()
	command := exec.CommandContext(callCtx, p.Binary, "app-server", "proxy", "--sock", p.SocketPath)
	stdin, err := command.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	if err = command.Start(); err != nil {
		return fmt.Errorf("start Codex app-server proxy: %w", err)
	}
	defer func() {
		_ = stdin.Close()
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		_ = command.Wait()
	}()
	return callback(&jsonRPCClient{writer: stdin, reader: bufio.NewReader(stdout)}, callCtx)
}

type jsonRPCClient struct {
	writer io.Writer
	reader *bufio.Reader
	next   int
	mu     sync.Mutex
}

func (c *jsonRPCClient) notify(ctx context.Context, method string, params map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	encoded, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	if err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	_, err = c.writer.Write(append(encoded, '\n'))
	return err
}

func (c *jsonRPCClient) call(ctx context.Context, method string, params map[string]any) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next++
	id := c.next
	encoded, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	if _, err = c.writer.Write(append(encoded, '\n')); err != nil {
		return nil, err
	}
	type response struct {
		ID     int             `json:"id"`
		Result map[string]any  `json:"result"`
		Error  json.RawMessage `json:"error"`
	}
	responseCh := make(chan struct {
		response
		err error
	}, 1)
	go func() {
		for {
			line, readErr := c.reader.ReadBytes('\n')
			if readErr != nil {
				responseCh <- struct {
					response
					err error
				}{err: readErr}
				return
			}
			var reply response
			if err := json.Unmarshal(line, &reply); err != nil {
				continue
			}
			if reply.ID == id {
				responseCh <- struct {
					response
					err error
				}{response: reply}
				return
			}
		}
	}()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case received := <-responseCh:
		if received.err != nil {
			return nil, received.err
		}
		if len(received.Error) > 0 && string(received.Error) != "null" {
			return nil, fmt.Errorf("Codex app-server %s failed", method)
		}
		if received.Result == nil {
			return map[string]any{}, nil
		}
		return received.Result, nil
	}
}
