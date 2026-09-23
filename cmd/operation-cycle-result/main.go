// Command operation-cycle-result submits a reviewed, sanitized terminal result
// to the local operation-runner Unix control socket. It never contacts CRM.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
)

const maxResultBytes = 256 << 10

type request struct {
	RequestID   string         `json:"request_id"`
	EventType   string         `json:"event_type"`
	Result      map[string]any `json:"result"`
	FailureCode string         `json:"failure_code,omitempty"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("operation-cycle-result", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	socket := flags.String("socket", "", "absolute operation-runner Unix socket")
	requestID := flags.String("request-id", "", "operation action request ID")
	resultFile := flags.String("result-file", "", "absolute sanitized result JSON file")
	eventType := flags.String("event-type", "completed", "completed or failed")
	failureCode := flags.String("failure-code", "", "required for failed")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid operation-cycle result arguments")
	}
	if !filepath.IsAbs(*socket) || strings.TrimSpace(*socket) != *socket || !validValue(*requestID) || !filepath.IsAbs(*resultFile) || strings.TrimSpace(*resultFile) != *resultFile || (*eventType != "completed" && *eventType != "failed") || (*eventType == "failed" && !validValue(*failureCode)) {
		return errors.New("invalid operation-cycle result input")
	}
	file, err := os.Open(*resultFile)
	if err != nil {
		return errors.New("result file is unavailable")
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxResultBytes+1))
	decoder.UseNumber()
	result := map[string]any{}
	if err = decoder.Decode(&result); err != nil {
		return errors.New("result JSON must be an object")
	}
	if err = decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("result JSON must contain one object")
	}
	if len(result) == 0 {
		return errors.New("result JSON must not be empty")
	}
	message, err := json.Marshal(request{RequestID: *requestID, EventType: *eventType, Result: result, FailureCode: *failureCode})
	if err != nil || len(message) > maxResultBytes {
		return errors.New("result is too large")
	}
	connection, err := net.Dial("unix", *socket)
	if err != nil {
		return errors.New("operation runner control socket is unavailable")
	}
	defer connection.Close()
	if _, err = connection.Write(message); err != nil {
		return errors.New("operation runner control socket rejected the result")
	}
	if err = connection.(*net.UnixConn).CloseWrite(); err != nil {
		return errors.New("operation runner control socket rejected the result")
	}
	var reply struct {
		OK bool `json:"ok"`
	}
	if err = json.NewDecoder(io.LimitReader(connection, 64<<10)).Decode(&reply); err != nil || !reply.OK {
		return errors.New("operation runner rejected the result")
	}
	_, err = fmt.Fprintln(stdout, `{"ok":true}`)
	return err
}

func validValue(value string) bool {
	return strings.TrimSpace(value) == value && len(value) > 0 && len(value) <= 200 && !strings.ContainsAny(value, "\r\n\t")
}
