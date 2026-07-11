#!/usr/bin/env bash
set -euo pipefail

green_REPO="${green_REPO:-BlusceLabs/green}"
green_VERSION="${green_VERSION:-latest}"
green_INSTALL_DIR="${green_INSTALL_DIR:-$HOME/.local/bin}"
green_GITHUB_API="${green_GITHUB_API:-https://api.github.com}"
green_GITHUB_BASE_URL="${green_GITHUB_BASE_URL:-https://github.com}"

usage() {
  cat <<'EOF'
Install green from GitHub Releases.

Usage:
  scripts/install.sh [--version <version>] [--repo <owner/repo>] [--install-dir <path>]

Environment:
  green_VERSION          Release version or tag. Defaults to latest.
  green_REPO             GitHub repository. Defaults to BlusceLabs/green.
  green_INSTALL_DIR      Directory for the green binary. Defaults to ~/.local/bin.
  green_GITHUB_API       GitHub API base URL. Defaults to https://api.github.com.
  green_GITHUB_BASE_URL  GitHub web base URL. Defaults to https://github.com.
EOF
}

fail() {
  echo "green install: $*" >&2
  exit 1
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --version)
      [ "$#" -ge 2 ] || fail "--version requires a value"
      green_VERSION="$2"
      shift 2
      ;;
    --repo)
      [ "$#" -ge 2 ] || fail "--repo requires a value"
      green_REPO="$2"
      shift 2
      ;;
    --install-dir)
      [ "$#" -ge 2 ] || fail "--install-dir requires a value"
      green_INSTALL_DIR="$2"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "unknown argument: $1"
      ;;
  esac
done

need_command() {
  command -v "$1" >/dev/null 2>&1 || fail "$1 is required"
}

download() {
  local url="$1"
  local output="$2"

  if command -v curl >/dev/null 2>&1; then
    curl --fail --location --show-error --silent "$url" --output "$output"
  elif command -v wget >/dev/null 2>&1; then
    wget --quiet "$url" --output-document "$output"
  else
    fail "curl or wget is required"
  fi
}

download_json() {
  local url="$1"
  local output="$2"

  if command -v curl >/dev/null 2>&1; then
    curl --fail --location --show-error --silent --header 'Accept: application/vnd.github+json' "$url" --output "$output"
  elif command -v wget >/dev/null 2>&1; then
    wget --quiet --header='Accept: application/vnd.github+json' "$url" --output-document "$output"
  else
    fail "curl or wget is required"
  fi
}

detect_platform() {
  case "$(uname -s)" in
    Linux) echo "linux" ;;
    Darwin) echo "macos" ;;
    *) fail "unsupported platform: $(uname -s)" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "x64" ;;
    arm64|aarch64) echo "arm64" ;;
    *) fail "unsupported architecture: $(uname -m)" ;;
  esac
}

latest_tag() {
  local metadata_file="$1"
  local api_url="${green_GITHUB_API%/}/repos/${green_REPO}/releases/latest"
  local tag

  download_json "$api_url" "$metadata_file"
  tag="$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$metadata_file" | head -n 1)"
  [ -n "$tag" ] || fail "could not read tag_name from $api_url"
  echo "$tag"
}

verify_checksum() {
  local checksum_file="$1"

  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 -c "$checksum_file"
  elif command -v sha256sum >/dev/null 2>&1; then
    sha256sum -c "$checksum_file"
  else
    fail "shasum or sha256sum is required"
  fi
}

find_extracted_entry() {
  local root="$1"
  local name="$2"
  local kind="$3"
  local candidate

  if [ "$kind" = "dir" ] && [ -d "$root/$name" ]; then
    echo "$root/$name"
    return 0
  fi
  if [ "$kind" = "file" ] && [ -f "$root/$name" ]; then
    echo "$root/$name"
    return 0
  fi

  for candidate in "$root"/*/"$name"; do
    if [ "$kind" = "dir" ] && [ -d "$candidate" ]; then
      echo "$candidate"
      return 0
    fi
    if [ "$kind" = "file" ] && [ -f "$candidate" ]; then
      echo "$candidate"
      return 0
    fi
  done

  return 1
}

find_extracted_binary() {
  find_extracted_entry "$1" "green" "file"
}

copy_optional_file() {
  local name="$1"
  local source_path

  if source_path="$(find_extracted_entry "$extract_dir" "$name" "file")"; then
    cp "$source_path" "$green_INSTALL_DIR/$name"
    chmod 755 "$green_INSTALL_DIR/$name"
  fi
}

copy_optional_dir() {
  local name="$1"
  local source_path

  if source_path="$(find_extracted_entry "$extract_dir" "$name" "dir")"; then
    rm -rf "$green_INSTALL_DIR/$name"
    cp -R "$source_path" "$green_INSTALL_DIR/$name"
  fi
}

need_command uname
need_command sed
need_command tar
need_command mktemp

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/green-install.XXXXXX")"
cleanup() {
  rm -rf "$tmp_dir"
}
trap cleanup EXIT

if [ "$green_VERSION" = "latest" ]; then
  tag="$(latest_tag "$tmp_dir/latest.json")"
else
  case "$green_VERSION" in
    v*) tag="$green_VERSION" ;;
    *) tag="v$green_VERSION" ;;
  esac
fi

version="${tag#v}"
platform="$(detect_platform)"
arch="$(detect_arch)"
archive_name="green-v${version}-${platform}-${arch}.tar.gz"
checksum_name="${archive_name}.sha256"
release_url="${green_GITHUB_BASE_URL%/}/${green_REPO}/releases/download/${tag}"
archive_path="$tmp_dir/$archive_name"
checksum_path="$tmp_dir/$checksum_name"
extract_dir="$tmp_dir/extract"

echo "Installing green ${tag} for ${platform}-${arch}"
download "${release_url}/${archive_name}" "$archive_path"
download "${release_url}/${checksum_name}" "$checksum_path"

(
  cd "$tmp_dir"
  verify_checksum "$checksum_name"
)

mkdir -p "$extract_dir"
tar -xzf "$archive_path" -C "$extract_dir"

binary_path="$(find_extracted_binary "$extract_dir")" || fail "release archive did not contain green"

mkdir -p "$green_INSTALL_DIR"
cp "$binary_path" "$green_INSTALL_DIR/green"
chmod 755 "$green_INSTALL_DIR/green"
copy_optional_file "green-linux-sandbox"
copy_optional_file "green-seccomp"
copy_optional_dir "helpers"

echo "Installed $green_INSTALL_DIR/green"

case ":$PATH:" in
  *":$green_INSTALL_DIR:"*) ;;
  *) echo "Add $green_INSTALL_DIR to PATH to run green from any directory." ;;
esac
