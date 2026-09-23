package compiler

import (
	"crypto/sha256"
	"encoding/json"
	"errors"

	segmentdsl "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/dsl"
	segmentport "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/port"
)

var ErrInvalid = errors.New("audience definition cannot be compiled")

type Compiler struct{}

func (Compiler) Compile(raw json.RawMessage) (segmentport.Definition, error) {
	ast, err := segmentdsl.Parse(raw)
	if err != nil {
		return segmentport.Definition{}, ErrInvalid
	}
	expression, err := json.Marshal(ast)
	if err != nil {
		return segmentport.Definition{}, ErrInvalid
	}
	return segmentport.Definition{SchemaVersion: 1, Expression: expression, Digest: sha256.Sum256(expression)}, nil
}
