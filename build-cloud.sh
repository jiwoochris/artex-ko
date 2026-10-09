#!/usr/bin/env bash
set -euo pipefail

# 1. 프론트엔드(Next.js) 의존성 설치 및 정적 빌드
echo "[*] Building Frontend..."
cd web
npm ci
npm run build:static
cd ..

# 2. 빌드된 프론트엔드 결과물을 Go 가 내장(embed)할 수 있는 위치로 복사
echo "[*] Copying static assets..."
mkdir -p server/webui/dist
cp -r web/out/* server/webui/dist/

# 3. Go 단일 바이너리 컴파일 (embedui 태그 필수)
echo "[*] Compiling Go Backend..."
CGO_ENABLED=0 go build -tags embedui -o artex ./cmd/artex

echo "[+] Build Completed successfully!"
