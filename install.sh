#!/bin/sh
# Download a released Webda CLI without a Go SDK.
set -eu

usage() {
    cat <<'HELP'
Install Webda CLI for macOS or Linux (amd64/arm64).

Usage: sh install.sh [options]

  --server URL          Configure the installed client for this server
  --name NAME           Command and profile name (default: webda.cli)
  --install-dir DIR     Destination directory (default: ~/.local/bin)
  --version VERSION     Release tag or version (default: latest)
  --install-completion  Explicitly install shell completion (requires --server)
  -h, --help            Show this help

Examples:
  sh install.sh
  sh install.sh --server https://app.example.com --name myapp
  sh install.sh --version v1.2.0 --install-dir "$HOME/bin"

Requires curl and sha256sum or shasum. Does not sign in or open a browser.
HELP
}

fail() { printf 'Error: %s\n' "$*" >&2; exit 1; }
require_value() {
    if [ "$#" -lt 2 ] || [ -z "$2" ]; then
        fail "$1 requires a value"
    fi
}

main() {
    name=webda.cli
    server=
    install_dir=
    version=latest
    completion=false
    while [ "$#" -gt 0 ]; do
        case "$1" in
            --server) require_value "$@"; server=$2; shift 2 ;;
            --name) require_value "$@"; name=$2; shift 2 ;;
            --install-dir) require_value "$@"; install_dir=$2; shift 2 ;;
            --version) require_value "$@"; version=$2; shift 2 ;;
            --install-completion) completion=true; shift ;;
            -h|--help) usage; return ;;
            *) fail "unknown option $1; use --help" ;;
        esac
    done
    case "$name" in
        ''|update|[-.]*|*[!a-zA-Z0-9._-]*) fail "--name must be a command name using letters, numbers, dots, underscores, or hyphens" ;;
    esac
    if [ "$completion" = true ] && [ -z "$server" ]; then
        fail "--install-completion requires --server"
    fi
    if [ -n "$server" ]; then
        case "$server" in http://*|https://*) ;; *) fail "--server must be an http(s) URL" ;; esac
        authority=${server#*://}
        authority=${authority%%/*}
        case "$authority" in ''|*@*) fail "--server must include a host and must not contain credentials" ;; esac
        case "$server" in *'?'*|*'#'*|*\\*|*[[:space:]]*) fail "--server must not contain credentials, whitespace, a query, or a fragment" ;; esac
    fi
    if [ -z "$install_dir" ]; then
        [ -n "${HOME:-}" ] || fail "HOME is unset; supply --install-dir"
        install_dir=$HOME/.local/bin
    fi
    case "$version" in
        latest) ;;
        *) case "$version" in v*) ;; *) version=v$version ;; esac
           case "$version" in v|*[!a-zA-Z0-9._-]*) fail "--version must be a release tag such as v1.2.0" ;; esac ;;
    esac
    case "$(uname -s)" in
        Darwin) os=darwin ;;
        Linux) os=linux ;;
        *) fail "unsupported operating system; this installer supports macOS and Linux" ;;
    esac
    case "$(uname -m)" in
        x86_64|amd64) arch=amd64 ;;
        arm64|aarch64) arch=arm64 ;;
        *) fail "unsupported architecture; this installer supports amd64 and arm64" ;;
    esac
    command -v curl >/dev/null 2>&1 || fail "curl is required"
    if command -v sha256sum >/dev/null 2>&1; then
        hash_command=sha256sum
    elif command -v shasum >/dev/null 2>&1; then
        hash_command=shasum
    else
        fail "sha256sum or shasum is required to verify the download"
    fi

    repository=https://github.com/loopingz/webda.cli
    if [ "$version" = latest ]; then
        # Resolve once so the binary and checksum file come from the same tag.
        release_url=$(curl -fsSL --retry 3 --connect-timeout 15 --max-time 120 \
            --output /dev/null --write-out '%{url_effective}' "$repository/releases/latest") \
            || fail "cannot resolve the latest release; check your connection or supply --version"
        case "$release_url" in
            "$repository/releases/tag/"*) version=${release_url##*/} ;;
            *) fail "latest release did not resolve to a GitHub release tag" ;;
        esac
        case "$version" in ''|*[!a-zA-Z0-9._-]*) fail "invalid latest release tag" ;; esac
    fi
    asset=webda-cli-$os-$arch
    release_base=$repository/releases/download/$version
    work_dir=$(mktemp -d "${TMPDIR:-/tmp}/webda-cli-install.XXXXXX") || fail "cannot create temporary directory"
    staged_binary=
    trap cleanup 0
    trap 'exit 130' INT
    trap 'exit 143' TERM

    printf 'Installing %s (%s/%s)...\n' "$version" "$os" "$arch"
    download "$release_base/checksums.txt" "$work_dir/checksums.txt" \
        || fail "release $version has no downloadable checksums; choose a release with installer support"
    expected=$(awk -v asset="$asset" '$2 == asset || $2 == "*" asset {print $1}' "$work_dir/checksums.txt")
    [ "${#expected}" -eq 64 ] || fail "release checksums do not contain a valid entry for $asset"
    case "$expected" in *[!a-fA-F0-9]*) fail "invalid checksum for $asset" ;; esac
    download "$release_base/$asset" "$work_dir/$asset" || fail "cannot download $asset from release $version"
    if [ "$hash_command" = sha256sum ]; then
        actual=$(sha256sum "$work_dir/$asset" | awk '{print $1}')
    else
        actual=$(shasum -a 256 "$work_dir/$asset" | awk '{print $1}')
    fi
    [ "$actual" = "$expected" ] || fail "checksum mismatch; the installed client was not changed"

    mkdir -p "$install_dir" || fail "cannot create $install_dir; choose a writable --install-dir"
    install_dir=$(cd "$install_dir" && pwd -P)
    target=$install_dir/$name
    [ ! -d "$target" ] || fail "$target is a directory"
    staged_binary=$(mktemp "$install_dir/.$name.install.XXXXXX") || fail "cannot stage the binary in $install_dir"
    cp "$work_dir/$asset" "$staged_binary"
    chmod 755 "$staged_binary"
    mv -f "$staged_binary" "$target"
    staged_binary=
    printf 'Installed %s\n' "$target"

    if [ -n "$server" ]; then
        if [ "$completion" = true ]; then
            "$target" setup --name "$name" --url "$server" --install-completion \
                || fail "binary installed, but server setup failed; run '$target setup' to finish"
        else
            "$target" setup --name "$name" --url "$server" \
                || fail "binary installed, but server setup failed; run '$target setup' to finish"
        fi
    fi
    case ":${PATH:-}:" in
        *":$install_dir:"*) ;;
        *) printf '\nAdd the install directory to PATH for this shell:\n'
           quoted_dir=$(printf '%s' "$install_dir" | sed "s/'/'\\\\''/g")
           printf '  export PATH='
           printf "'%s'" "$quoted_dir"
           # Print a command for the user's shell, preserving its PATH expansion.
           # shellcheck disable=SC2016
           printf ':"$PATH"\n' ;;
    esac
    if [ -n "$server" ]; then
        printf '\nRun %s explore to sign in and browse operations.\n' "$name"
    else
        printf '\nRun %s setup to configure a server.\n' "$name"
    fi
}

download() {
    curl -fsSL --retry 3 --connect-timeout 15 --max-time 120 --output "$2" "$1"
}
cleanup() {
    # Both paths are created by mktemp during this invocation.
    rm -rf "$work_dir"
    if [ -n "$staged_binary" ]; then rm -f "$staged_binary"; fi
}

# Keep execution at the end so a piped download must include all function bodies.
main "$@"
