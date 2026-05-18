package cli

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestErrorEnvelopeShape(t *testing.T) {
	var stderr bytes.Buffer
	exit := writeError(&stderr, notFoundErr("issue gone"), true)
	if exit != ExitNotFound {
		t.Fatalf("exit = %d, want %d", exit, ExitNotFound)
	}
	var got struct {
		Code    string `json:"code"`
		Exit    int    `json:"exit"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(stderr.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, stderr.String())
	}
	if got.Code != "not_found" || got.Exit != 3 || got.Message != "issue gone" {
		t.Fatalf("envelope: %+v", got)
	}
}

func TestErrorEnvelopePlaintextFallback(t *testing.T) {
	var stderr bytes.Buffer
	exit := writeError(&stderr, validationErr("bad input"), false)
	if exit != ExitValidation {
		t.Fatalf("exit = %d, want %d", exit, ExitValidation)
	}
	if got := stderr.String(); got != "bad input\n" {
		t.Fatalf("plaintext stderr = %q", got)
	}
}
