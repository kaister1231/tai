package main

import (
	"io"
	"os"
	"strings"
)

type style struct{ color bool }

func newStyle(w io.Writer) style {
	file, ok := w.(*os.File)
	if !ok || !isTerminal(file) || os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return style{}
	}
	return style{color: true}
}

func (s style) wrap(code, text string) string {
	if !s.color || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s style) command(text string) string { return s.wrap("1;36", text) }
func (s style) dim(text string) string     { return s.wrap("2", text) }
func (s style) prompt(text string) string  { return s.wrap("1;33", text) }
func (s style) code(text string) string    { return s.wrap("36", text) }
func (s style) bold(text string) string    { return s.wrap("1", text) }

// answer escapes untrusted model text first, then renders `code` and **bold**
// spans so the markers never show up raw in the terminal.
func (s style) answer(text string) string {
	safe := safeTerminal(strings.TrimSpace(text))
	var out strings.Builder
	for i := 0; i < len(safe); {
		switch {
		case safe[i] == '`':
			if end := strings.IndexByte(safe[i+1:], '`'); end > 0 && !strings.Contains(safe[i+1:i+1+end], "\n") {
				out.WriteString(s.code(safe[i+1 : i+1+end]))
				i += end + 2
				continue
			}
		case strings.HasPrefix(safe[i:], "**"):
			if end := strings.Index(safe[i+2:], "**"); end > 0 && !strings.Contains(safe[i+2:i+2+end], "\n") {
				out.WriteString(s.bold(safe[i+2 : i+2+end]))
				i += end + 4
				continue
			}
		}
		out.WriteByte(safe[i])
		i++
	}
	return out.String()
}
