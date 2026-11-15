package main

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

var version = "dev"

const (
	releasesURL       = "https://github.com/kaister1231/tai/releases"
	maxArchiveBytes   = 64 << 20
	maxChecksumsBytes = 64 << 10
)

var releaseTagPattern = regexp.MustCompile(`^v[0-9A-Za-z.+-]+$`)

func currentVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && strings.HasPrefix(info.Main.Version, "v") {
		return info.Main.Version
	}
	return version
}

type updater struct {
	http    *http.Client
	baseURL string
	exePath string
	current string
	goos    string
	goarch  string
	stdout  io.Writer
}

func runUpdate(ctx context.Context, stdout, stderr io.Writer) int {
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		fmt.Fprintf(stderr, "tai: 无法确定当前程序路径：%s\n", safeTerminal(err.Error()))
		return 1
	}
	u := updater{
		http:    &http.Client{Timeout: 2 * time.Minute},
		baseURL: releasesURL,
		exePath: exe,
		current: currentVersion(),
		goos:    runtime.GOOS,
		goarch:  runtime.GOARCH,
		stdout:  stdout,
	}
	if err := u.run(ctx); err != nil {
		fmt.Fprintf(stderr, "tai: 更新失败：%s\n", safeTerminal(err.Error()))
		return 1
	}
	return 0
}

func (u updater) run(ctx context.Context) error {
	tag, err := u.latestTag(ctx)
	if err != nil {
		return err
	}
	if tag == u.current {
		fmt.Fprintf(u.stdout, "已是最新版本 %s。\n", tag)
		return nil
	}
	name := fmt.Sprintf("tai-%s-%s", u.goos, u.goarch)
	fmt.Fprintf(u.stdout, "正在从 %s 更新到 %s（%s）...\n", u.current, tag, name)
	archive, err := u.download(ctx, tag, name+".tar.gz", maxArchiveBytes)
	if err != nil {
		return err
	}
	sums, err := u.download(ctx, tag, "SHA256SUMS", maxChecksumsBytes)
	if err != nil {
		return err
	}
	if err := verifyChecksum(archive, sums, name+".tar.gz"); err != nil {
		return err
	}
	binary, err := extractBinary(archive, name+"/tai")
	if err != nil {
		return err
	}
	if err := replaceExecutable(u.exePath, binary); err != nil {
		return err
	}
	fmt.Fprintf(u.stdout, "已更新到 %s：%s\n", tag, safeTerminal(u.exePath))
	return nil
}

// latestTag follows the releases/latest redirect instead of the rate-limited REST API.
func (u updater) latestTag(ctx context.Context) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, u.baseURL+"/latest", nil)
	if err != nil {
		return "", err
	}
	response, err := u.http.Do(request)
	if err != nil {
		return "", fmt.Errorf("无法连接 GitHub：%w", err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("查询最新版本失败（HTTP %d）", response.StatusCode)
	}
	tag := path.Base(response.Request.URL.Path)
	if !releaseTagPattern.MatchString(tag) {
		return "", fmt.Errorf("没有找到已发布的版本")
	}
	return tag, nil
}

func (u updater) download(ctx context.Context, tag, file string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.baseURL+"/download/"+tag+"/"+file, nil)
	if err != nil {
		return nil, err
	}
	response, err := u.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("下载 %s 失败：%w", file, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载 %s 失败（HTTP %d）", file, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("下载 %s 失败：%w", file, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s 超过大小上限", file)
	}
	return data, nil
}

func verifyChecksum(data, sums []byte, file string) error {
	scanner := bufio.NewScanner(strings.NewReader(string(sums)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == file {
			sum := sha256.Sum256(data)
			if !strings.EqualFold(fields[0], hex.EncodeToString(sum[:])) {
				return errors.New("SHA256 校验失败")
			}
			return nil
		}
	}
	return fmt.Errorf("SHA256SUMS 中没有 %s", file)
}

func extractBinary(archive []byte, member string) ([]byte, error) {
	gz, err := gzip.NewReader(strings.NewReader(string(archive)))
	if err != nil {
		return nil, fmt.Errorf("安装包格式错误：%w", err)
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("安装包中没有 %s", member)
		}
		if err != nil {
			return nil, fmt.Errorf("安装包格式错误：%w", err)
		}
		if path.Clean(header.Name) == member && header.Typeflag == tar.TypeReg {
			data, err := io.ReadAll(io.LimitReader(reader, maxArchiveBytes+1))
			if err != nil {
				return nil, fmt.Errorf("解压失败：%w", err)
			}
			if len(data) > maxArchiveBytes || len(data) == 0 {
				return nil, errors.New("安装包中的程序大小异常")
			}
			return data, nil
		}
	}
}

func replaceExecutable(target string, binary []byte) error {
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".tai-update-*")
	if err != nil {
		return fmt.Errorf("无法写入 %s（%w）；可改用安装脚本或用有权限的用户运行", dir, err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(binary); err != nil {
		tmp.Close()
		return fmt.Errorf("写入新版本失败：%w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("写入新版本失败：%w", err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return fmt.Errorf("设置执行权限失败：%w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("替换 %s 失败：%w", target, err)
	}
	return nil
}
