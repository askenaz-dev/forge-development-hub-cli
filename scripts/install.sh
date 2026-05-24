#!/usr/bin/env bash
#
# fdh installer for macOS and Linux.
#
# Usage:
#   curl -fsSL https://${FDH_PKG_HOST}/fdh/install.sh | bash
#   curl -fsSL https://${FDH_PKG_HOST}/fdh/install.sh | bash -s -- --version v0.5.2
#
# Env vars:
#   FDH_PKG_HOST   override the download host
#                  (default: pkg.forge.internal — placeholder until
#                  the platform team confirms the real host).
#   FDH_INSTALL_DIR override the install directory
#                  (default: $HOME/.fdh/bin).
#
# Exit codes (stable):
#   0  success
#   1  generic error
#   2  invalid usage
#   3  unsupported OS/arch
#   4  network error fetching manifest or binary
#   5  checksum mismatch
#
# The script is idempotent: re-running with the same target version is a no-op
# if the on-disk binary already hashes to the expected SHA-256.

set -euo pipefail

# Stable defaults — the placeholder host is the contract-level default per
# the fdh-cli-implementation-contract spec; the real host overrides via env.
DEFAULT_HOST="pkg.forge.internal"
FDH_PKG_HOST="${FDH_PKG_HOST:-${DEFAULT_HOST}}"
FDH_INSTALL_DIR="${FDH_INSTALL_DIR:-${HOME}/.fdh/bin}"
VERSION="latest"

# --- arg parsing ---------------------------------------------------------

usage() {
    cat <<EOF
fdh installer

Usage:
  install.sh [--version <vX.Y.Z>] [--help]

Env:
  FDH_PKG_HOST    Override the download host (default: ${DEFAULT_HOST})
  FDH_INSTALL_DIR Override install directory (default: \$HOME/.fdh/bin)
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --version)
            shift
            VERSION="${1:-}"
            if [[ -z "${VERSION}" ]]; then
                echo "error: --version requires an argument" >&2
                exit 2
            fi
            ;;
        --help|-h)
            usage
            exit 0
            ;;
        *)
            echo "error: unknown flag: $1" >&2
            usage >&2
            exit 2
            ;;
    esac
    shift
done

if [[ "${FDH_PKG_HOST}" == "${DEFAULT_HOST}" ]]; then
    echo "warning: FDH_PKG_HOST not set; using placeholder default '${DEFAULT_HOST}'." >&2
    echo "         Set FDH_PKG_HOST to the real forge host before deploying." >&2
fi

# --- OS + arch detection (task 7.1) --------------------------------------

uname_s="$(uname -s)"
uname_m="$(uname -m)"

case "${uname_s}" in
    Darwin)  os="darwin" ;;
    Linux)   os="linux" ;;
    *)
        echo "error: unsupported OS: ${uname_s}" >&2
        exit 3
        ;;
esac

case "${uname_m}" in
    x86_64|amd64) arch="amd64" ;;
    arm64|aarch64) arch="arm64" ;;
    *)
        echo "error: unsupported arch: ${uname_m}" >&2
        exit 3
        ;;
esac

target="${os}-${arch}"

# --- helpers -------------------------------------------------------------

need_cmd() {
    if ! command -v "$1" >/dev/null 2>&1; then
        echo "error: required tool '$1' not found on PATH" >&2
        exit 1
    fi
}

need_cmd curl
need_cmd tar

# Pick the right sha-256 binary: macOS ships shasum; Linux ships sha256sum.
sha256_of() {
    local file="$1"
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "${file}" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "${file}" | awk '{print $1}'
    else
        echo "error: neither sha256sum nor shasum is available" >&2
        exit 1
    fi
}

# --- resolve version (task 7.3) ------------------------------------------

manifest_url="https://${FDH_PKG_HOST}/fdh/manifest.json"
manifest_json=""

fetch_manifest() {
    # `curl -fsSL` fails fast on HTTP errors and follows redirects.
    if ! manifest_json="$(curl -fsSL "${manifest_url}")"; then
        echo "error: could not fetch manifest at ${manifest_url}" >&2
        exit 4
    fi
}

resolve_version() {
    if [[ "${VERSION}" != "latest" ]]; then
        return
    fi
    fetch_manifest
    # Tiny JSON parser; the manifest shape is:
    #   { "latest": "v0.5.2", "versions": { ... } }
    # We avoid jq to keep this script dependency-free.
    VERSION="$(printf '%s' "${manifest_json}" | sed -n 's/.*"latest"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)"
    if [[ -z "${VERSION}" ]]; then
        echo "error: manifest does not declare a 'latest' field" >&2
        exit 4
    fi
}

resolve_version

# --- discover artifact URLs ---------------------------------------------

# Convention published by .goreleaser.yaml + the manifest publisher:
#   /fdh/<version>/fdh_<version>_<os>_<arch>.tar.gz
#   /fdh/<version>/fdh_<version>_<os>_<arch>.tar.gz.sha256
# Versions in the URL keep the leading "v".
artifact="fdh_${VERSION}_${os}_${arch}.tar.gz"
url_base="https://${FDH_PKG_HOST}/fdh/${VERSION}"
artifact_url="${url_base}/${artifact}"
sha_url="${artifact_url}.sha256"

echo "fdh installer"
echo "  target:   ${target}"
echo "  version:  ${VERSION}"
echo "  host:     ${FDH_PKG_HOST}"
echo "  install:  ${FDH_INSTALL_DIR}"

# --- idempotency check (task 7.8) ----------------------------------------

bin_path="${FDH_INSTALL_DIR}/fdh"

if [[ -f "${bin_path}" ]]; then
    expected_sha="$(curl -fsSL "${sha_url}" 2>/dev/null | awk '{print $1}' || true)"
    # The .sha256 file's content is the hash of the .tar.gz, not the binary
    # itself. Compare hashes only when we have a recent local copy of the
    # tarball — otherwise just trust the binary check below to re-download.
    if [[ -n "${expected_sha}" && -f "${FDH_INSTALL_DIR}/.last-tarball-sha256" ]]; then
        if [[ "$(cat "${FDH_INSTALL_DIR}/.last-tarball-sha256")" == "${expected_sha}" ]]; then
            echo "already up-to-date: ${bin_path}"
            exit 0
        fi
    fi
fi

# --- download + verify + extract (tasks 7.4, 7.5) ------------------------

tmpdir="$(mktemp -d)"
trap 'rm -rf "${tmpdir}"' EXIT

tarball="${tmpdir}/${artifact}"
shafile="${tarball}.sha256"

echo "downloading ${artifact_url}"
if ! curl -fsSL --retry 3 -o "${tarball}" "${artifact_url}"; then
    echo "error: download failed: ${artifact_url}" >&2
    exit 4
fi
if ! curl -fsSL --retry 3 -o "${shafile}" "${sha_url}"; then
    echo "error: download of checksum failed: ${sha_url}" >&2
    exit 4
fi

expected_sha="$(awk '{print $1}' "${shafile}")"
actual_sha="$(sha256_of "${tarball}")"
if [[ "${expected_sha}" != "${actual_sha}" ]]; then
    echo "error: checksum mismatch for ${artifact}" >&2
    echo "  expected: ${expected_sha}" >&2
    echo "  actual:   ${actual_sha}" >&2
    exit 5
fi
echo "checksum ok"

mkdir -p "${FDH_INSTALL_DIR}"
tar -xzf "${tarball}" -C "${tmpdir}"

# The tarball's top-level may be either "fdh" directly (single binary) or
# a versioned directory containing fdh. Locate the binary.
extracted_bin=""
if [[ -f "${tmpdir}/fdh" ]]; then
    extracted_bin="${tmpdir}/fdh"
else
    extracted_bin="$(find "${tmpdir}" -mindepth 1 -maxdepth 3 -name fdh -type f -print -quit || true)"
fi
if [[ -z "${extracted_bin}" ]]; then
    echo "error: 'fdh' binary not found inside ${artifact}" >&2
    exit 1
fi

install -m 0755 "${extracted_bin}" "${bin_path}"
printf '%s\n' "${expected_sha}" > "${FDH_INSTALL_DIR}/.last-tarball-sha256"
echo "installed ${bin_path}"

# --- PATH editing (tasks 7.6, 7.7) ---------------------------------------

path_line='export PATH="$HOME/.fdh/bin:$PATH"'

shell_name="$(basename -- "${SHELL:-}")"
rc_file=""

case "${shell_name}" in
    zsh)
        rc_file="${HOME}/.zshrc"
        ;;
    bash)
        # Linux conventionally uses .bashrc; macOS bash users sometimes have
        # .bash_profile. Prefer whichever exists; default to .bashrc.
        if [[ -f "${HOME}/.bashrc" ]]; then
            rc_file="${HOME}/.bashrc"
        elif [[ -f "${HOME}/.bash_profile" ]]; then
            rc_file="${HOME}/.bash_profile"
        else
            rc_file="${HOME}/.bashrc"
        fi
        ;;
    fish|nu)
        echo
        echo "note: detected ${shell_name} shell; add this manually to your shell config:"
        echo "  set -gx PATH \$HOME/.fdh/bin \$PATH    (fish)"
        echo "  let-env PATH = (\$env.PATH | prepend '\$HOME/.fdh/bin')  (nushell)"
        echo
        exit 0
        ;;
    *)
        echo
        echo "note: unrecognised shell '${shell_name}'; add ${FDH_INSTALL_DIR} to your PATH manually."
        exit 0
        ;;
esac

if [[ -f "${rc_file}" ]] && grep -F -q '/.fdh/bin' "${rc_file}"; then
    echo "PATH already configured in ${rc_file}"
else
    {
        echo ""
        echo "# Added by fdh installer (https://${FDH_PKG_HOST}/fdh/install.sh)"
        echo "${path_line}"
    } >> "${rc_file}"
    echo "added PATH entry to ${rc_file}"
    echo "reload your shell or run: source ${rc_file}"
fi

echo
echo "All set. Run:  fdh --version"
