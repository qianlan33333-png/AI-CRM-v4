package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	controlLimit       = 256 << 10
	controlReadTimeout = 10 * time.Second
)

// ServeControl is the local completion entrypoint. It is deliberately a Unix
// socket: Codex sends a sanitized terminal result to the same local runner;
// neither the socket nor a local file path is exposed to CRM.
func (r *Runner) ServeControl(ctx context.Context, socketPath string) error {
	return r.serveControl(ctx, socketPath, nil)
}

// serveControl signals readiness only after a private 0600 socket is bound.
// A caller can therefore refuse to claim any external work when local terminal
// receipt is unavailable.
func (r *Runner) serveControl(ctx context.Context, socketPath string, ready chan<- error) error {
	signalReady := func(err error) {
		if ready != nil {
			ready <- err
		}
	}
	if r == nil || !filepath.IsAbs(socketPath) || strings.TrimSpace(socketPath) != socketPath {
		err := errors.New("operation runner control socket is invalid")
		signalReady(err)
		return err
	}
	if err := prepareControlSocket(socketPath); err != nil {
		signalReady(err)
		return err
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		err = fmt.Errorf("listen operation runner control socket: %w", err)
		signalReady(err)
		return err
	}
	if err = os.Chmod(socketPath, 0600); err != nil {
		_ = listener.Close()
		_ = os.Remove(socketPath)
		signalReady(err)
		return err
	}
	signalReady(nil)
	defer func() { _ = listener.Close(); _ = os.Remove(socketPath) }()
	go func() { <-ctx.Done(); _ = listener.Close() }()
	for {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			if ctx.Err() != nil {
				return nil
			}
			return acceptErr
		}
		go r.handleControl(ctx, connection)
	}
}

// prepareControlSocket removes only a stale private socket. A live listener,
// any non-socket node, or an owner/mode mismatch is never deleted.
func prepareControlSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return errors.New("operation runner control socket path already exists")
	}
	if info.Mode().Perm() != 0600 {
		return errors.New("operation runner control socket permissions are unsafe")
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || int(stat.Uid) != os.Geteuid() {
		return errors.New("operation runner control socket owner is unsafe")
	}
	connection, dialErr := net.DialTimeout("unix", path, 200*time.Millisecond)
	if dialErr == nil {
		_ = connection.Close()
		return errors.New("operation runner control socket is already active")
	}
	return os.Remove(path)
}

func (r *Runner) handleControl(ctx context.Context, connection net.Conn) {
	defer connection.Close()
	_ = connection.SetReadDeadline(time.Now().Add(controlReadTimeout))
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-done:
		}
	}()
	var message struct {
		RequestID   string         `json:"request_id"`
		EventType   string         `json:"event_type"`
		Result      map[string]any `json:"result"`
		FailureCode string         `json:"failure_code"`
	}
	decoder := json.NewDecoder(io.LimitReader(connection, controlLimit))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&message); err != nil {
		controlReply(connection, err)
		return
	}
	action, ok := r.action(strings.TrimSpace(message.RequestID))
	if !ok {
		controlReply(connection, errors.New("operation action is not locally active"))
		return
	}
	var err error
	switch message.EventType {
	case "completed":
		if message.Result == nil {
			err = errors.New("completion result is required")
		} else {
			err = r.Complete(ctx, action, message.Result)
		}
	case "failed":
		if message.Result == nil || strings.TrimSpace(message.FailureCode) == "" {
			err = errors.New("failure result and code are required")
		} else {
			err = r.remote.Event(ctx, ActionEvent{RequestID: action.RequestID, EventID: action.RequestID + ":failed", EventType: "failed", LeaseToken: action.LeaseToken, Result: message.Result, FailureCode: message.FailureCode})
			if err == nil {
				r.forget(action.RequestID)
			}
		}
	default:
		err = errors.New("unsupported control event")
	}
	controlReply(connection, err)
}
func controlReply(connection net.Conn, err error) {
	_ = connection.SetWriteDeadline(time.Now().Add(controlReadTimeout))
	value := map[string]any{"ok": err == nil}
	if err != nil {
		value["error"] = "control request rejected"
	}
	_ = json.NewEncoder(connection).Encode(value)
}
