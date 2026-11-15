package main

import (
	"strings"
	"testing"
)

func TestStyleAnswerRendersInlineMarkersWithoutColor(t *testing.T) {
	got := style{}.answer("  路径是 `~/.local/bin`，**只有 1 个**。\n未闭合 ` 保留  ")
	want := "路径是 ~/.local/bin，只有 1 个。\n未闭合 ` 保留"
	if got != want {
		t.Fatalf("answer = %q, want %q", got, want)
	}
}

func TestStyleAnswerColorsCodeAndEscapesModelControlCharacters(t *testing.T) {
	got := style{color: true}.answer("运行 `ls` \x1b[31m危险")
	if !strings.Contains(got, "\x1b[36mls\x1b[0m") {
		t.Fatalf("inline code was not highlighted: %q", got)
	}
	if strings.Contains(got, "\x1b[31m") || !strings.Contains(got, "\\x1b[31m") {
		t.Fatalf("model escape sequence was not neutralized: %q", got)
	}
}
