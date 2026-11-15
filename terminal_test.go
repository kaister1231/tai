package main

import (
	"strings"
	"testing"
)

func TestSafeTerminalEscapesTerminalControls(t *testing.T) {
	got := safeTerminal("\x1b[31mred\x1b[0m\r\u202e")
	want := "\\x1b[31mred\\x1b[0m\\x0d\\u202e"
	if got != want {
		t.Fatalf("safeTerminal() = %q, want %q", got, want)
	}
}

func TestCommandValidationRejectsDisplayControls(t *testing.T) {
	for _, command := range []string{"printf x\nrm -rf .", "printf '\x1b[2J'", "echo \u202e"} {
		if err := validateCommand(command); err == nil {
			t.Errorf("validateCommand(%q) accepted unsafe command", command)
		}
	}
	if err := validateCommand("grep -n 'hello' file.txt"); err != nil {
		t.Fatalf("valid command rejected: %v", err)
	}
	if err := validateCommand(strings.Repeat("x", 8193)); err == nil {
		t.Fatal("oversized command accepted")
	}
}
