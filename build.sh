#!/bin/sh
# Builds the OnionOS package: dist/Cyberspace.miyoo-v<version>-OnionOS.zip
set -e
cd "$(dirname "$0")"
VERSION=$(grep -o 'const version = "[^"]*"' main.go | cut -d'"' -f2)
[ -f go.work ] || go mod tidy
go test ./...
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -trimpath -ldflags="-s -w" -o package/Cyberspace.miyoo/cyberspace .
go run . -icon package/Cyberspace.miyoo/icon.png
rm -rf dist && mkdir -p dist/stage/App
cp -r package/Cyberspace.miyoo dist/stage/App/
rm -rf dist/stage/App/Cyberspace.miyoo/data
(cd dist/stage && zip -qr "../Cyberspace.miyoo-v$VERSION-OnionOS.zip" App)
rm -rf dist/stage
echo "Built dist/Cyberspace.miyoo-v$VERSION-OnionOS.zip"
