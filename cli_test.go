package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestReadLineContextRejectsAndDrainsOversizedInput(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("yes-but-too-long\nno\n"))
	if _, err := readLineContext(context.Background(), reader, 3); !errors.Is(err, errInputLineTooLong) {
		t.Fatalf("oversized line error = %v, want %v", err, errInputLineTooLong)
	}
	line, err := readLineContext(context.Background(), reader, 3)
	if err != nil || line != "no" {
		t.Fatalf("next line = %q, %v; want the complete next line", line, err)
	}
}

func TestNonTTYAndExplainModesNeverExecute(t *testing.T) {
	for _, test := range []struct {
		name       string
		args       []string
		isTerminal bool
		input      string
	}{
		{name: "piped stdin", args: []string{"analyze"}, input: "yes\npiped data"},
		{name: "explain", args: []string{"--explain", "suggest a command"}, isTerminal: true, input: "yes\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := t.TempDir() + "/must-not-exist"
			command, _ := json.Marshal(Action{Type: "command", Command: "touch " + path, Explanation: "创建一个空文件。"})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				writeChatResponse(w, string(command))
			}))
			defer server.Close()
			t.Setenv("TAI_BASE_URL", server.URL+"/v1")
			t.Setenv("TAI_MODEL", "test")
			t.Setenv("TAI_API_KEY", "")
			t.Setenv("OPENAI_API_KEY", "")
			var stdout, stderr strings.Builder
			code := runCLI(context.Background(), test.args, strings.NewReader(test.input), test.isTerminal, &stdout, &stderr, newDefaultClient)
			if code != 0 {
				t.Fatalf("runCLI returned %d: %s", code, stderr.String())
			}
			if !strings.Contains(stdout.String(), "仅供参考，不会执行") || strings.Contains(stdout.String(), "要执行以上命令吗？") {
				t.Fatalf("suggestion was not clearly answer-only: %s", stdout.String())
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("command unexpectedly created %s", path)
			}
		})
	}
}

func TestOversizedConfirmationCannotApproveCommand(t *testing.T) {
	path := t.TempDir() + "/must-not-exist"
	command, _ := json.Marshal(Action{Type: "command", Command: "touch " + path, Explanation: "创建一个空文件。"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeChatResponse(w, string(command))
	}))
	defer server.Close()
	t.Setenv("TAI_BASE_URL", server.URL+"/v1")
	t.Setenv("TAI_MODEL", "test")
	t.Setenv("TAI_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	input := strings.NewReader(strings.Repeat("y", maxConfirmationLineBytes+1) + "\ny\n")
	var stdout, stderr strings.Builder
	code := runCLI(context.Background(), []string{"question"}, input, true, &stdout, &stderr, newDefaultClient)
	if code != 0 {
		t.Fatalf("runCLI returned %d: %s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "整行已丢弃") {
		t.Fatalf("oversized confirmation was not clearly rejected: %s", stderr.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("oversized confirmation unexpectedly created %s", path)
	}
}

func TestCLIHelpDoesNotRequireAPIKey(t *testing.T) {
	t.Setenv("TAI_BASE_URL", "https://example.invalid/v1")
	t.Setenv("TAI_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")
	var stdout, stderr strings.Builder
	code := runCLI(context.Background(), []string{"--help"}, strings.NewReader(""), false, &stdout, &stderr, nil)
	if code != 0 || !strings.Contains(stdout.String(), "用自然语言询问") {
		t.Fatalf("help failed: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
