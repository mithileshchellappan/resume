#!/usr/bin/env bash
set -euo pipefail

REPO="mithileshchellappan/resume"
BIN_NAME="resume"

DEFAULT_INSTALL_DIR="/usr/local/bin"
if [ -n "${HOME:-}" ]; then
  DEFAULT_INSTALL_DIR="${HOME}/.local/bin"
fi
INSTALL_DIR="${INSTALL_DIR:-${DEFAULT_INSTALL_DIR}}"

OS="$(uname -s)"
ARCH="$(uname -m)"

case "${OS}" in
  Darwin) OS="macOS" ;;
  Linux) OS="linux" ;;
  *)
    echo "Unsupported OS: ${OS}"
    exit 1
    ;;
esac

case "${ARCH}" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)
    echo "Unsupported architecture: ${ARCH}"
    exit 1
    ;;
esac

LATEST_URL="$(curl -fsSL -o /dev/null -w "%{url_effective}" "https://github.com/${REPO}/releases/latest")"
TAG="${LATEST_URL##*/}"
VERSION="${TAG#v}"

if [ -z "${TAG}" ] || [ "${TAG}" = "latest" ]; then
  echo "Could not determine latest release tag."
  exit 1
fi

ASSET="${BIN_NAME}_${VERSION}_${OS}_${ARCH}"
if [ "${OS}" = "windows" ]; then
  ASSET="${ASSET}.exe"
fi
BASE_URL="https://github.com/${REPO}/releases/download/${TAG}"
BIN_URL="${BASE_URL}/${ASSET}"
CHECKSUMS_URL="${BASE_URL}/${BIN_NAME}_${VERSION}_checksums.txt"

TMP_DIR="$(mktemp -d)"
trap 'rm -rf "${TMP_DIR}"' EXIT

echo "Downloading ${ASSET}..."
curl -fsSL "${BIN_URL}" -o "${TMP_DIR}/${ASSET}"

if curl -fsSL "${CHECKSUMS_URL}" -o "${TMP_DIR}/checksums.txt"; then
  if command -v shasum >/dev/null 2>&1 || command -v sha256sum >/dev/null 2>&1; then
    EXPECTED="$(grep -E "[ *]${ASSET}$" "${TMP_DIR}/checksums.txt" | awk '{print $1}')"
    if [ -n "${EXPECTED}" ]; then
      if command -v shasum >/dev/null 2>&1; then
        ACTUAL="$(shasum -a 256 "${TMP_DIR}/${ASSET}" | awk '{print $1}')"
      else
        ACTUAL="$(sha256sum "${TMP_DIR}/${ASSET}" | awk '{print $1}')"
      fi

      if [ "${EXPECTED}" != "${ACTUAL}" ]; then
        echo "Checksum verification failed."
        exit 1
      fi

      echo "Checksum verified."
    else
      echo "Warning: asset entry not found in checksums file. Skipping verification."
    fi
  fi
fi

if ! mkdir -p "${INSTALL_DIR}" 2>/dev/null; then
  if command -v sudo >/dev/null 2>&1; then
    sudo mkdir -p "${INSTALL_DIR}"
  else
    echo "Cannot create ${INSTALL_DIR}. Set INSTALL_DIR or run with sudo."
    exit 1
  fi
fi

TARGET="${INSTALL_DIR}/${BIN_NAME}"
if [ -w "${INSTALL_DIR}" ]; then
  install -m 755 "${TMP_DIR}/${ASSET}" "${TARGET}"
else
  if command -v sudo >/dev/null 2>&1; then
    sudo install -m 755 "${TMP_DIR}/${ASSET}" "${TARGET}"
  else
    echo "Cannot write to ${INSTALL_DIR}. Set INSTALL_DIR or run with sudo."
    exit 1
  fi
fi

echo "Installed ${BIN_NAME} to ${TARGET}"
echo "Run: ${BIN_NAME} --help"
