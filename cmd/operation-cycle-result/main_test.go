package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunSubmitsReviewedResultToControlledUnixSocket(t *testing.T) {
	directory := t.TempDir()
	file, err := os.CreateTemp("/tmp", "aicrm-operation-result-")
	if err != nil {
		t.Fatal(err)
	}
	socket := file.Name()
	if err = file.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(socket); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	resultPath := filepath.Join(directory, "result.json")
	if err = os.WriteFile(resultPath, []byte(`{"summary":"complete","count":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	received := make(chan request, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		var message request
		if json.NewDecoder(connection).Decode(&message) == nil {
			received <- message
		}
		_, _ = connection.Write([]byte(`{"ok":true}\n`))
	}()
	var output strings.Builder
	if err = run([]string{"--socket", socket, "--request-id", "ocact_0123456789012345678901234567", "--result-file", resultPath}, &output); err != nil {
		t.Fatal(err)
	}
	message := <-received
	if message.EventType != "completed" || message.Result["summary"] != "complete" || output.String() != "{\"ok\":true}\n" {
		t.Fatalf("message=%#v output=%q", message, output.String())
	}
}

func TestRunRejectsRelativeResultPathsBeforeDialing(t *testing.T) {
	if err := run([]string{"--socket", "relative.sock", "--request-id", "ocact_0123456789012345678901234567", "--result-file", "result.json"}, &strings.Builder{}); err == nil {
		t.Fatal("relative local paths accepted")
	}
}
