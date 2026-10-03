package main

import (
	"strings"
	"testing"
)

func TestRecoveryStdinRejectsAmbiguousAndUnexpectedSecrets(t *testing.T) {
	for _, text := range []string{
		`{"password":"first","password":"second","session_token":"token"}`,
		`{"password":"first","session_token":"one","session_token":"two"}`,
		`{"password":"first","session_token":"token","admin":true}`,
		`{"password":"first","session_token":"token"} {}`,
		`{"password":123,"session_token":"token"}`,
		`{"password":"first","session_token":null}`,
		`{"password":"first"}`, `[]`, `null`,
		`{"password":"first","session_token":"token"}` + strings.Repeat(" ", 1<<20) + `{}`,
	} {
		if _, err := readSecrets(strings.NewReader(text)); err == nil {
			t.Fatal("ambiguous or invalid stdin accepted")
		}
	}
	got, err := readSecrets(strings.NewReader(`{"password":"  exact-secret  ","session_token":"token"}`))
	if err != nil || got.Password != "  exact-secret  " || got.SessionToken != "token" {
		t.Fatal("valid stdin was changed or rejected")
	}
}
