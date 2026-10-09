#!/usr/bin/env bash
# BODA cross-platform release builder.
#
# The default mode builds one target and embeds the already-exported frontend.
# `./build.sh --release` builds and packages all supported desktop/server targets.
#
# Environment variables:
#   BODA_TARGET_OS=linux             One target OS in single-target mode.
#   BODA_TARGET_ARCH=amd64           One target arch in single-target mode.
#   BODA_TARGETS=linux/amd64,...     Comma-separated targets for multi-target mode.
#   BODA_BUILD_VERSION=v0.3.3        Version embedded in the binary and archive name.
#   BODA_OUTPUT=/path/to/boda       Explicit binary path in single-target mode.
#   BODA_OUTPUT_DIR=dist             Directory for default binary paths.
#   BODA_PACKAGE=1                   Create a zip archive for each target.
#   BODA_PACKAGE_DIR=dist            Directory for release archives.
#   BODA_COMPRESS=off                UPX mode: off, auto, or required.
#   BODA_UPX_ARGS="--best --lzma"    Arguments passed to UPX.
#   BODA_SKIP_FRONTEND=1             Reuse server/webui/dist (for CI artifact builds).
#   BODA_SKIP_NPM_CI=1               Skip npm ci while rebuilding the frontend.
#   BODA_GOSUMDB=sum.golang.org      Go checksum database.
set -euo pipefail

cd "$(cd "$(dirname "$0")" && pwd)"

info() { printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok() { printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn() { printf '\033[33m[!]\033[0m %s\n' "$*" >&2; }
die() { printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
사용법:
  ./build.sh                         현재 시스템·현재 아키텍처로 컴파일
  ./build.sh --target linux/amd64   지정한 대상 하나를 컴파일
  ./build.sh --release               지원하는 모든 대상을 컴파일하고 패키징

옵션:
  --release              Linux, macOS, Windows 의 amd64/arm64 대상을 빌드하고 zip 생성
  --target OS/ARCH       단일 대상을 지정합니다. 예: windows/amd64
  --upx                  UPX 로 바이너리를 강제 압축합니다(일부 Linux 환경에서 호환성에 영향을 줄 수 있습니다)
  --no-compress          UPX 를 쓰지 않고 Go linker 로만 축소한 뒤 zip 을 압축
  --help                 도움말 표시

여러 대상 목록은 BODA_TARGETS 로 재정의할 수 있습니다. 예:
  BODA_TARGETS=linux/amd64,windows/amd64 ./build.sh --release
EOF
}

RELEASE_TARGETS_DEFAULT="linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64"
BODA_RELEASE="${BODA_RELEASE:-0}"
BODA_COMPRESS="${BODA_COMPRESS:-off}"
BODA_PACKAGE="${BODA_PACKAGE:-0}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --release)
      BODA_RELEASE=1
      BODA_PACKAGE=1
      shift
      ;;
    --target)
      [ "$#" -ge 2 ] || die "--target 에는 OS/ARCH 인자가 필요합니다"
      target_arg="$2"
      case "$target_arg" in
        */*)
          BODA_TARGET_OS="${target_arg%%/*}"
          BODA_TARGET_ARCH="${target_arg##*/}"
          BODA_TARGETS="$target_arg"
          ;;
        *) die "대상은 OS/ARCH 형식이어야 합니다. 예: linux/amd64" ;;
      esac
      shift 2
      ;;
    --no-compress)
      BODA_COMPRESS=0
      shift
      ;;
    --upx)
      BODA_COMPRESS=required
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *) die "알 수 없는 인자입니다: $1(사용법은 --help 로 확인하세요)" ;;
  esac
done

command -v go >/dev/null 2>&1 || die "Go 를 찾을 수 없습니다(이 프로젝트는 Go 1.26 이상이 필요합니다)"

BODA_GOSUMDB="${BODA_GOSUMDB:-sum.golang.org}"
if [ -z "${BODA_BUILD_VERSION:-}" ]; then
  if command -v git >/dev/null 2>&1 && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    BODA_BUILD_VERSION="$(git describe --tags --always --dirty)"
  else
    BODA_BUILD_VERSION="dev"
  fi
fi
# Release tags are commonly passed as v0.3.3; keep the binary version consistent.
BODA_BUILD_VERSION="${BODA_BUILD_VERSION#v}"
BODA_OUTPUT_DIR="${BODA_OUTPUT_DIR:-dist}"
BODA_PACKAGE_DIR="${BODA_PACKAGE_DIR:-$BODA_OUTPUT_DIR}"
BODA_UPX_ARGS="${BODA_UPX_ARGS:---best --lzma}"

if [ "${BODA_RELEASE}" = "1" ]; then
  BODA_TARGETS="${BODA_TARGETS:-$RELEASE_TARGETS_DEFAULT}"
else
  BODA_TARGET_OS="${BODA_TARGET_OS:-$(GOSUMDB="$BODA_GOSUMDB" go env GOOS)}"
  BODA_TARGET_ARCH="${BODA_TARGET_ARCH:-$(GOSUMDB="$BODA_GOSUMDB" go env GOARCH)}"
  BODA_TARGETS="${BODA_TARGETS:-${BODA_TARGET_OS}/${BODA_TARGET_ARCH}}"
fi

if [ "${BODA_SKIP_FRONTEND:-0}" = "1" ]; then
  [ -d server/webui/dist ] || die "BODA_SKIP_FRONTEND=1 이지만 server/webui/dist 가 없습니다"
else
  command -v npm >/dev/null 2>&1 || die "npm 을 찾을 수 없습니다(프런트엔드 정적 빌드에는 Node.js/npm 이 필요합니다)"
  command -v rsync >/dev/null 2>&1 || die "rsync 를 찾을 수 없습니다"
  info "프런트엔드 정적 리소스를 빌드합니다"
  if [ "${BODA_SKIP_NPM_CI:-0}" != "1" ]; then
    (cd web && npm ci)
  fi
  (cd web && npm run build:static)
  info "프런트엔드 리소스를 server/webui/dist 로 동기화합니다"
  mkdir -p server/webui/dist
  rsync -a --delete web/out/ server/webui/dist/
fi

compress_binary() {
  binary="$1"
  goos="$2"
  case "$BODA_COMPRESS" in
    0|off|false|none)
      info "UPX 를 건너뜁니다: $binary"
      return 0
      ;;
    auto|required|true|1) ;;
    *) die "BODA_COMPRESS 는 off, auto, required 중 하나여야 합니다" ;;
  esac

  if ! command -v upx >/dev/null 2>&1; then
    if [ "$BODA_COMPRESS" = "required" ]; then
      die "BODA_COMPRESS=required 이지만 upx 를 찾을 수 없습니다"
    fi
    warn "upx 를 찾을 수 없어 linker 압축 결과를 그대로 둡니다: $binary"
    return 0
  fi

  before=$(wc -c < "$binary" | tr -d ' ')
  upx_args="$BODA_UPX_ARGS"
  [ "$goos" = "darwin" ] && upx_args="$upx_args --force-macos"
  # shellcheck disable=SC2086
  if ! upx $upx_args -- "$binary"; then
    if [ "$BODA_COMPRESS" = "required" ]; then
      die "UPX 압축에 실패했습니다: $binary"
    fi
    warn "UPX 가 이 대상 형식을 지원하지 않아 압축하지 않은 바이너리를 그대로 둡니다: $binary"
    return 0
  fi
  after=$(wc -c < "$binary" | tr -d ' ')
  ok "UPX 압축 완료: $binary (${before} -> ${after} bytes)"
}

package_binary() {
  binary="$1"
  goos="$2"
  goarch="$3"
  package_name="boda-${BODA_BUILD_VERSION}-${goos}-${goarch}"
  package_root="${BODA_PACKAGE_DIR}/${package_name}"
  archive="${BODA_PACKAGE_DIR}/${package_name}.zip"

  command -v zip >/dev/null 2>&1 || die "패키징에는 zip 이 필요합니다"
  rm -rf "$package_root" "$archive"
  mkdir -p "$package_root"
  cp "$binary" "$package_root/"
  # 데몬 시작 스크립트가 정식 진입점입니다. 화면의 원클릭 업데이트는 프로세스가 종료된 뒤 이 스크립트가 다시 띄워 줘야 동작하고,
  # boda 본체를 직접 실행하면 업데이트 후 다시 기동되지 않습니다. 대상 시스템에 맞는 한 벌만 포함합니다.
  if [ "$goos" = "windows" ]; then
    cp start.bat "$package_root/"
  else
    cp start.sh "$package_root/"
    chmod +x "$package_root/start.sh"
  fi
  cp -R skills "$package_root/"
  cp config.example.json "$package_root/"
  if [ -f README.md ]; then cp README.md "$package_root/"; fi
  (cd "$BODA_PACKAGE_DIR" && zip -q -r -9 "$(basename "$archive")" "$(basename "$package_root")")
  rm -rf "$package_root"
  ok "릴리스 압축 파일: $archive"
}

build_target() {
  target="$1"
  case "$target" in
    */*) ;;
    *) die "잘못된 대상입니다: $target(OS/ARCH 형식이어야 합니다)" ;;
  esac
  goos="${target%%/*}"
  goarch="${target##*/}"
  case "$goos" in
    linux|darwin|windows) ;;
    *) die "지원하지 않는 시스템입니다: $goos(linux, darwin, windows 를 지원합니다)" ;;
  esac

  binary_name="boda"
  [ "$goos" = "windows" ] && binary_name="boda.exe"
  if [ -n "${BODA_OUTPUT:-}" ] && [ "$BODA_RELEASE" != "1" ]; then
    output="$BODA_OUTPUT"
  else
    output="${BODA_OUTPUT_DIR}/boda-${goos}-${goarch}/${binary_name}"
  fi
  mkdir -p "$(dirname "$output")"

  info "${goos}/${goarch} 컴파일, 버전 ${BODA_BUILD_VERSION}"
  GOSUMDB="$BODA_GOSUMDB" \
  CGO_ENABLED=0 \
  GOOS="$goos" \
  GOARCH="$goarch" \
  go build \
    -tags embedui \
    -trimpath \
    -ldflags "-s -w -buildid= -X main.version=${BODA_BUILD_VERSION}" \
    -o "$output" \
    ./cmd/boda

  compress_binary "$output" "$goos"
  if command -v file >/dev/null 2>&1; then file "$output"; fi
  if [ "$BODA_PACKAGE" = "1" ]; then package_binary "$output" "$goos" "$goarch"; fi
  ok "컴파일 완료: $output"
}

write_checksums() {
  [ "$BODA_PACKAGE" = "1" ] || return 0
  checksum_file="$BODA_PACKAGE_DIR/SHA256SUMS"
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$BODA_PACKAGE_DIR" && for archive in *.zip; do sha256sum "$archive"; done > "$(basename "$checksum_file")")
  elif command -v shasum >/dev/null 2>&1; then
    (cd "$BODA_PACKAGE_DIR" && for archive in *.zip; do shasum -a 256 "$archive"; done > "$(basename "$checksum_file")")
  else
    warn "sha256sum 또는 shasum 을 찾을 수 없어 SHA256SUMS 를 건너뜁니다"
    return 0
  fi
  ok "체크섬 파일: $checksum_file"
}

mkdir -p "$BODA_OUTPUT_DIR"
if [ "$BODA_PACKAGE" = "1" ]; then mkdir -p "$BODA_PACKAGE_DIR"; fi

old_ifs="$IFS"
IFS=','
read -r -a targets <<< "$BODA_TARGETS"
IFS="$old_ifs"
[ "${#targets[@]}" -gt 0 ] || die "BODA_TARGETS 는 비워 둘 수 없습니다"
for target in "${targets[@]}"; do
  target="${target//[[:space:]]/}"
  [ -n "$target" ] || continue
  build_target "$target"
done

if [ "$BODA_PACKAGE" = "1" ]; then
  write_checksums
  info "릴리스 패키지를 생성했습니다: $BODA_PACKAGE_DIR"
fi
