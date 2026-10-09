#!/usr/bin/env bash
set -euo pipefail

# Render는 PORT 환경변수를 임의로 할당하므로, 해당 포트로 앱이 켜지도록 인자 지정
# 기본 실행 및 스킬/데이터 경로 유지
: "${PORT:?PORT 환경변수가 설정되어 있어야 합니다}"
echo "[*] Starting ARTEX Server on port :$PORT"
exec ./artex -addr ":$PORT"
