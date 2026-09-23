// wecom-archive-sdk-runner confines the vendor C SDK and its dlopen handles
// to a short-lived child process. The SDK's stdout/stderr is redirected away
// from the framed protocol; no provider plaintext or diagnostics are logged.
package main

import (
	"bufio"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/archivesdk"
	"os"
	"syscall"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "--stdio" {
		os.Exit(2)
	}
	protocol, ok := protocolOutput()
	if !ok {
		os.Exit(2)
	}
	defer protocol.Close()
	// The Go runtime does not promise to invoke C library destructors at process
	// exit. Record the native allocation balance explicitly after every normal
	// request so the Linux fixture verifies the production free path.
	defer nativeShutdown()
	var request archivesdk.Request
	if err := archivesdk.ReadFrame(bufio.NewReader(os.Stdin), &request); err != nil {
		os.Exit(2)
	}
	response := run(request)
	w := bufio.NewWriter(protocol)
	_ = archivesdk.WriteFrame(w, response)
	_ = w.Flush()
}
func protocolOutput() (*os.File, bool) {
	fd, err := syscall.Dup(int(os.Stdout.Fd()))
	if err != nil {
		return nil, false
	}
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		_ = syscall.Close(fd)
		return nil, false
	}
	if err = syscall.Dup2(int(devnull.Fd()), int(os.Stdout.Fd())); err == nil {
		err = syscall.Dup2(int(devnull.Fd()), int(os.Stderr.Fd()))
	}
	_ = devnull.Close()
	if err != nil {
		_ = syscall.Close(fd)
		return nil, false
	}
	return os.NewFile(uintptr(fd), "archive-sdk-protocol"), true
}
func run(request archivesdk.Request) archivesdk.Response { return nativeRun(request) }
