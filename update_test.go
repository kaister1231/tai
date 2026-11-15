package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func releaseArchive(t *testing.T, member string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: member, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func fakeReleaseServer(t *testing.T, tag string, archive []byte, sums string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/releases/tag/"+tag, http.StatusFound)
	})
	mux.HandleFunc("/releases/tag/"+tag, func(w http.ResponseWriter, r *http.Request) {})
	mux.HandleFunc("/releases/download/"+tag+"/tai-linux-arm64.tar.gz", func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	mux.HandleFunc("/releases/download/"+tag+"/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, sums) })
	return httptest.NewServer(mux)
}

func testUpdater(t *testing.T, server *httptest.Server, current string) (updater, string, *strings.Builder) {
	exe := filepath.Join(t.TempDir(), "tai")
	if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	out := &strings.Builder{}
	return updater{http: server.Client(), baseURL: server.URL + "/releases", exePath: exe, current: current, goos: "linux", goarch: "arm64", stdout: out}, exe, out
}

func TestUpdateReplacesExecutableAfterChecksum(t *testing.T) {
	archive := releaseArchive(t, "tai-linux-arm64/tai", []byte("new-binary"))
	sum := sha256.Sum256(archive)
	server := fakeReleaseServer(t, "v9.9.9", archive, hex.EncodeToString(sum[:])+"  tai-linux-arm64.tar.gz\n")
	defer server.Close()
	u, exe, out := testUpdater(t, server, "v0.1.0")
	if err := u.run(context.Background()); err != nil {
		t.Fatalf("update: %v", err)
	}
	data, _ := os.ReadFile(exe)
	info, _ := os.Stat(exe)
	if string(data) != "new-binary" || info.Mode().Perm() != 0o755 || !strings.Contains(out.String(), "v9.9.9") {
		t.Fatalf("binary=%q mode=%v out=%q", data, info.Mode(), out.String())
	}
}

func TestUpdateRejectsChecksumMismatch(t *testing.T) {
	archive := releaseArchive(t, "tai-linux-arm64/tai", []byte("tampered"))
	server := fakeReleaseServer(t, "v9.9.9", archive, strings.Repeat("0", 64)+"  tai-linux-arm64.tar.gz\n")
	defer server.Close()
	u, exe, _ := testUpdater(t, server, "v0.1.0")
	if err := u.run(context.Background()); err == nil || !strings.Contains(err.Error(), "SHA256") {
		t.Fatalf("expected checksum error, got %v", err)
	}
	if data, _ := os.ReadFile(exe); string(data) != "old" {
		t.Fatalf("executable changed after failed update: %q", data)
	}
}

func TestUpdateSkipsWhenAlreadyLatest(t *testing.T) {
	server := fakeReleaseServer(t, "v0.1.0", nil, "")
	defer server.Close()
	u, exe, out := testUpdater(t, server, "v0.1.0")
	if err := u.run(context.Background()); err != nil || !strings.Contains(out.String(), "已是最新") {
		t.Fatalf("err=%v out=%q", err, out.String())
	}
	if data, _ := os.ReadFile(exe); string(data) != "old" {
		t.Fatalf("executable changed: %q", data)
	}
}
