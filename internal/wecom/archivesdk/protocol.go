// Package archivesdk defines the private framed protocol shared by the
// Message Archive adapter and its isolated SDK subprocess.
package archivesdk

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
)

const MaxFrame = 8 << 20

var ErrProtocol = errors.New("archive SDK protocol invalid")

type DecryptItem struct {
	DecryptKey       string `json:"decrypt_key"`
	EncryptedMessage string `json:"encrypted_message"`
}
type Request struct {
	Operation        string        `json:"operation"`
	LibraryPath      string        `json:"library_path"`
	CorpID           string        `json:"corp_id"`
	Secret           string        `json:"secret"`
	Seq              uint64        `json:"seq"`
	Limit            uint32        `json:"limit"`
	DecryptKey       string        `json:"decrypt_key"`
	EncryptedMessage string        `json:"encrypted_message"`
	DecryptItems     []DecryptItem `json:"decrypt_items,omitempty"`
	FileID           string        `json:"file_id"`
	IndexBuf         string        `json:"index_buf"`
}
type Response struct {
	ErrorCode       string   `json:"error_code,omitempty"`
	Data            []byte   `json:"data,omitempty"`
	Items           [][]byte `json:"items,omitempty"`
	NextIndexBuf    string   `json:"next_indexbuf,omitempty"`
	Finished        bool     `json:"finished,omitempty"`
	LibraryLoadable bool     `json:"library_loadable,omitempty"`
	HandleCreated   bool     `json:"handle_created,omitempty"`
}

func WriteFrame(w io.Writer, value any) error {
	raw, err := json.Marshal(value)
	if err != nil || len(raw) == 0 || len(raw) > MaxFrame {
		return ErrProtocol
	}
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], uint32(len(raw)))
	if _, err = w.Write(h[:]); err != nil {
		return err
	}
	_, err = w.Write(raw)
	return err
}
func ReadFrame(r io.Reader, value any) error {
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(h[:])
	if n == 0 || n > MaxFrame {
		return ErrProtocol
	}
	raw := make([]byte, n)
	if _, err := io.ReadFull(r, raw); err != nil {
		return err
	}
	if json.Unmarshal(raw, value) != nil {
		return ErrProtocol
	}
	return nil
}
func Call(ctx context.Context, runner string, request Request) (Response, error) {
	if runner == "" {
		return Response{}, ErrProtocol
	}
	cmd := exec.CommandContext(ctx, runner, "--stdio")
	in, err := cmd.StdinPipe()
	if err != nil {
		return Response{}, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return Response{}, err
	}
	if err = cmd.Start(); err != nil {
		return Response{}, err
	}
	w := bufio.NewWriter(in)
	if err = WriteFrame(w, request); err == nil {
		err = w.Flush()
	}
	_ = in.Close()
	var result Response
	if err == nil {
		err = ReadFrame(bufio.NewReader(out), &result)
	}
	waitErr := cmd.Wait()
	if err != nil {
		return Response{}, err
	}
	if waitErr != nil {
		return Response{}, waitErr
	}
	return result, nil
}
