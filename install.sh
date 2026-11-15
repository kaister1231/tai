#!/bin/sh
# Install or uninstall tai from GitHub Releases.
#   curl -fsSL https://raw.githubusercontent.com/kaister1231/tai/main/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/kaister1231/tai/main/install.sh | sh -s -- --uninstall
# Environment: TAI_VERSION (e.g. v0.1.0, default latest), TAI_INSTALL_DIR (default ~/.local/bin).
set -eu

repo="kaister1231/tai"
install_dir="${TAI_INSTALL_DIR:-$HOME/.local/bin}"
version="${TAI_VERSION:-latest}"
release_url="${TAI_RELEASE_URL:-}"

die() { echo "tai install: $*" >&2; exit 1; }

case "${1:-}" in
  "") ;;
  --uninstall)
    if [ -e "$install_dir/tai" ]; then
      rm -f "$install_dir/tai"
      echo "已卸载 $install_dir/tai"
    else
      echo "未找到 $install_dir/tai，无需卸载"
    fi
    exit 0 ;;
  -h|--help) echo "用法: install.sh [--uninstall]  环境变量: TAI_VERSION, TAI_INSTALL_DIR"; exit 0 ;;
  *) die "未知参数: $1（支持 --uninstall）" ;;
esac

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) die "不支持的系统: $(uname -s)" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) die "不支持的架构: $(uname -m)" ;;
esac

if [ -z "$release_url" ]; then
  if [ "$version" = latest ]; then
    release_url="https://github.com/$repo/releases/latest/download"
  else
    release_url="https://github.com/$repo/releases/download/$version"
  fi
fi

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL "$1" -o "$2"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q "$1" -O "$2"; }
else
  die "需要 curl 或 wget"
fi
if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
else
  die "需要 sha256sum 或 shasum 校验下载文件"
fi

name="tai-$os-$arch"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "下载 $name ($version)..."
fetch "$release_url/$name.tar.gz" "$tmp/$name.tar.gz" || die "下载失败: $release_url/$name.tar.gz"
fetch "$release_url/SHA256SUMS" "$tmp/SHA256SUMS" || die "下载校验文件失败"

expected="$(awk -v f="$name.tar.gz" '$2 == f || $2 == "*" f { print $1 }' "$tmp/SHA256SUMS")"
[ -n "$expected" ] || die "SHA256SUMS 中没有 $name.tar.gz"
[ "$(sha256 "$tmp/$name.tar.gz")" = "$expected" ] || die "SHA256 校验失败"

tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
[ -f "$tmp/$name/tai" ] || die "压缩包中没有 tai"
mkdir -p "$install_dir"
install -m 755 "$tmp/$name/tai" "$install_dir/tai.tmp"
mv -f "$install_dir/tai.tmp" "$install_dir/tai"
echo "已安装到 $install_dir/tai"

case ":$PATH:" in
  *":$install_dir:"*) ;;
  *) echo "提示: $install_dir 不在 PATH 中，请把下面这行加入 shell 配置文件:"
     echo "  export PATH=\"$install_dir:\$PATH\"" ;;
esac
