package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	ExitOK         = 0
	ExitInternal   = 1
	ExitUsage      = 2
	ExitNotFound   = 3
	ExitValidation = 4
	ExitConfig     = 5
)

type CLIError struct {
	Code    string
	ExitVal int
	Message string
	Wrapped error
}

func (e *CLIError) Error() string { return e.Message }
func (e *CLIError) Unwrap() error { return e.Wrapped }

func usageErr(msg string) error {
	return &CLIError{Code: "usage", ExitVal: ExitUsage, Message: msg}
}

func validationErr(msg string) error {
	return &CLIError{Code: "validation", ExitVal: ExitValidation, Message: msg}
}

func notFoundErr(msg string) error {
	return &CLIError{Code: "not_found", ExitVal: ExitNotFound, Message: msg}
}

func configErr(msg string) error {
	return &CLIError{Code: "config", ExitVal: ExitConfig, Message: msg}
}

func wrapErr(err error, code string, exitVal int) error {
	if err == nil {
		return nil
	}
	return &CLIError{Code: code, ExitVal: exitVal, Message: err.Error(), Wrapped: err}
}

func writeError(stderr io.Writer, err error, jsonOut bool) int {
	if err == nil {
		return ExitOK
	}
	var ce *CLIError
	if !errors.As(err, &ce) {
		ce = &CLIError{Code: "internal", ExitVal: ExitInternal, Message: err.Error(), Wrapped: err}
	}
	if jsonOut {
		enc := json.NewEncoder(stderr)
		enc.SetIndent("", "  ")
		_ = enc.Encode(struct {
			Code    string `json:"code"`
			Exit    int    `json:"exit"`
			Message string `json:"message"`
		}{Code: ce.Code, Exit: ce.ExitVal, Message: ce.Message})
	} else {
		fmt.Fprintln(stderr, ce.Message)
	}
	return ce.ExitVal
}

// errIsJSONFromArgs scans args for --json or --no-json so error formatting
// can match the chosen output style even when parsing failed.
func errIsJSONFromArgs(args []string) bool {
	for _, a := range args {
		switch {
		case a == "--no-json":
			return false
		case a == "--json", strings.HasPrefix(a, "--json="):
			return true
		}
	}
	return true
}
