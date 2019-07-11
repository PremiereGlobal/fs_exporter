#!/bin/bash
rm -rf build 
GO111MODULE=on CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags="-s -w -X main.version=${VERSION}" -a -o ./build/fs_exporter
