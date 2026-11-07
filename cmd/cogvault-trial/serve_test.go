package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"github.com/teslamint/cogvault/internal/trial"
	"github.com/teslamint/cogvault/internal/trialmcp"
)

func TestServeRejectsUnsafeOptionsBeforeOpeningRoot(t *testing.T) {
	for _, args := range [][]string{
		{"--transport", "http"},
		{"--transport", "http", "--auth-mode", "none"},
		{"--transport", "http", "--auth-mode", "oauth", "--public-url", "https://trial.example", "--oauth-audience", "https://trial.example/mcp"},
		{"--transport", "stdio", "--listen", "127.0.0.1:8081"},
		{"--transport", "stdio", "--auth-mode", "bearer"},
		{"--transport", "other"},
		{"extra"},
	} {
		err := serve(trial.Config{}, args, nil, strings.NewReader(""), io.Discard, io.Discard)
		if err == nil || strings.Contains(err.Error(), "root") {
			t.Errorf("%v did not fail on options: %v", args, err)
		}
	}
	for _, env := range [][]string{{bearerEnvironment + "=secret"}, {"COGVAULT_TRIALMCP_TYPO=x"}} {
		if err := serve(trial.Config{}, nil, env, strings.NewReader(""), io.Discard, io.Discard); err == nil {
			t.Fatal("stdio accepted remote environment")
		}
	}
}

func TestStdioSmokeHasOnlyTrialTools(t *testing.T) {
	env, _ := trialEnv(t)
	cfg, err := trial.FromEnviron(env)
	if err != nil {
		t.Fatal(err)
	}
	s, err := trial.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	var out bytes.Buffer
	if err := serveStdio(context.Background(), trialmcp.NewServer(s), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	var listed struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				InputSchema struct {
					AdditionalProperties bool                       `json:"additionalProperties"`
					Properties           map[string]json.RawMessage `json:"properties"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var response struct {
			ID int `json:"id"`
		}
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatal(err)
		}
		if response.ID == 2 {
			if err := json.Unmarshal([]byte(line), &listed); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(listed.Result.Tools) != 4 {
		t.Fatalf("unexpected inventory: %s", out.String())
	}
	names := map[string]bool{}
	for _, tool := range listed.Result.Tools {
		names[tool.Name] = true
		wantFields := 1
		if tool.Name == "trial_submit" {
			wantFields = 3
		}
		if tool.InputSchema.AdditionalProperties || len(tool.InputSchema.Properties) != wantFields {
			t.Fatalf("non-strict schema for %s", tool.Name)
		}
	}
	if !names["trial_submit"] || !names["trial_status"] || !names["trial_import_manifest"] || !names["trial_report_status"] {
		t.Fatalf("unexpected names: %v", names)
	}
	if !strings.Contains(out.String(), `"trial_submit"`) || !strings.Contains(out.String(), `"trial_status"`) || strings.Contains(out.String(), "wiki_") {
		t.Fatalf("bad stdio output: %s", out.String())
	}
}

func TestStdioBoundsInput(t *testing.T) {
	err := serveStdio(context.Background(), server.NewMCPServer("test", "1"), strings.NewReader(strings.Repeat("x", trialmcp.MaxRequestBytes+1)+"\n"), io.Discard)
	if err == nil {
		t.Fatal("accepted oversized line")
	}
}

func TestStdioEOFDrainsToolResponse(t *testing.T) {
	env, _ := trialEnv(t)
	cfg, err := trial.FromEnviron(env)
	if err != nil {
		t.Fatal(err)
	}
	s, err := trial.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// A tool call is queued to mcp-go's worker pool. EOF must not cancel the
	// context until that worker has written its response and exited.
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"trial_submit","arguments":{"url":"https://www.rfc-editor.org/rfc/rfc9110.pdf"}}}` + "\n"
	var out bytes.Buffer
	if err := serveStdio(context.Background(), trialmcp.NewServer(s), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	drained := false
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var response struct {
			ID     int `json:"id"`
			Result struct {
				IsError bool `json:"isError"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &response); err != nil {
			t.Fatal(err)
		}
		if response.ID == 2 {
			drained = true
			if response.Result.IsError || len(response.Result.Content) != 1 || !strings.Contains(response.Result.Content[0].Text, `"state":"queued"`) {
				t.Fatalf("tool response: %s", line)
			}
		}
	}
	if !drained {
		t.Fatalf("EOF lost tool response: %s", out.String())
	}
	activity, err := s.A.JobActivity(context.Background(), time.Time{})
	if err != nil || activity.Created != 1 || activity.Fetches != 0 {
		t.Fatalf("activity: %+v %v", activity, err)
	}
}
