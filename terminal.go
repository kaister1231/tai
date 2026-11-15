package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
	"unsafe"
)

func safeTerminal(value string) string {
	var out strings.Builder
	for _, r := range strings.ToValidUTF8(value, "�") {
		if r == '\n' || r == '\t' {
			out.WriteRune(r)
			continue
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Cs, r) || r == '\u2028' || r == '\u2029' {
			switch {
			case r <= 0xff:
				fmt.Fprintf(&out, "\\x%02x", r)
			case r <= 0xffff:
				fmt.Fprintf(&out, "\\u%04x", r)
			default:
				fmt.Fprintf(&out, "\\U%08x", r)
			}
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

func validateCommand(command string) error {
	if strings.TrimSpace(command) == "" {
		return fmt.Errorf("command is empty")
	}
	if len(command) > 8192 {
		return fmt.Errorf("command exceeds the 8192-byte limit")
	}
	for _, r := range command {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Cs, r) || r == '\u2028' || r == '\u2029' {
			return fmt.Errorf("command contains terminal or line control characters")
		}
	}
	return nil
}

func isTerminal(file *os.File) bool {
	if file == nil || (runtime.GOOS != "linux" && runtime.GOOS != "darwin") {
		return false
	}
	var term syscall.Termios
	request := uintptr(0x5401)
	if runtime.GOOS == "darwin" {
		request = 0x40487413
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, file.Fd(), request, uintptr(unsafe.Pointer(&term)))
	return errno == 0
}

func truncateInput(value string, maxBytes int) string {
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
