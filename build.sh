#!/bin/bash
rm  -rf build 
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -ldflags="-s -w" -o ./build/fs_exporter
