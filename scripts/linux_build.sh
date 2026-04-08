#!/bin/bash

go build \
    -trimpath \
    -ldflags="-extldflags -static -s -w" \
    -tags netgo \
    -o ./out/http2dns ./cmd/http2dns
