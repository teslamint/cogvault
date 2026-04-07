package main

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/spf13/cobra"
	"github.com/teslamint/cogvault/internal/index"
)

func testCmd() (*cobra.Command, *bytes.Buffer) {
	cmd := &cobra.Command{}
	errBuf := new(bytes.Buffer)
	cmd.SetErr(errBuf)
	return cmd, errBuf
}

func TestHandleConsistencyResult_Nil(t *testing.T) {
	cmd, errBuf := testCmd()
	if err := handleConsistencyResult(cmd, nil); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if errBuf.Len() != 0 {
		t.Errorf("expected no stderr, got %q", errBuf.String())
	}
}

func TestHandleConsistencyResult_Systemic(t *testing.T) {
	cmd, _ := testCmd()
	sysErr := fmt.Errorf("scan failed: %w", index.ErrConsistencySystemic)
	err := handleConsistencyResult(cmd, sysErr)
	if err == nil {
		t.Fatal("expected error for systemic failure")
	}
	if !errors.Is(err, index.ErrConsistencySystemic) {
		t.Errorf("expected ErrConsistencySystemic, got %v", err)
	}
}

func TestHandleConsistencyResult_PerFile(t *testing.T) {
	cmd, errBuf := testCmd()
	perFileErr := errors.New("read failed: permission denied")
	err := handleConsistencyResult(cmd, perFileErr)
	if err != nil {
		t.Errorf("expected nil for per-file error, got %v", err)
	}
	stderr := errBuf.String()
	if stderr == "" {
		t.Error("expected warning on stderr")
	}
	if !bytes.Contains(errBuf.Bytes(), []byte("warning")) {
		t.Errorf("expected 'warning' in stderr, got %q", stderr)
	}
}
