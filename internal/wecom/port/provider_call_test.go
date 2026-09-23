package port

import (
	"errors"
	"testing"
)

func TestProviderCallAttemptedIsConservative(t *testing.T) {
	if ProviderCallAttempted(WrapProviderWriteError(errors.New("token"), false)) {
		t.Fatal("token acquisition failure was classified as provider call")
	}
	if !ProviderCallAttempted(WrapProviderWriteError(errors.New("timeout"), true)) {
		t.Fatal("provider timeout was not classified as attempted")
	}
	if !ProviderCallAttempted(errors.New("unclassified adapter failure")) {
		t.Fatal("unclassified failure must fail safe as attempted")
	}
}

func TestProviderWriteDispositionDropsCodeWithoutCompletedAttempt(t *testing.T) {
	preCall := WrapProviderWriteDispositionWithCode(errors.New("token rejected"), false, false, false, 40003)
	if _, ok := ProviderErrorCode(preCall); ok {
		t.Fatal("pre-call failure exposed a Provider code")
	}
	unknown := WrapProviderWriteDispositionWithCode(errors.New("response lost"), true, true, false, 40003)
	if _, ok := ProviderErrorCode(unknown); ok {
		t.Fatal("unknown outcome exposed a Provider code")
	}
	if _, ok := ProviderErrorCode(&ProviderWriteError{Err: errors.New("manually malformed"), ErrorCode: 40003}); ok {
		t.Fatal("unattempted malformed Provider error exposed a code")
	}
	completed := WrapProviderWriteDispositionWithCode(errors.New("provider rejected"), true, false, false, -1)
	if code, ok := ProviderErrorCode(completed); !ok || code != -1 {
		t.Fatalf("completed response code=%d/%t", code, ok)
	}
}
