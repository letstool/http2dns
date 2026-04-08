@echo off
go build ^
    -trimpath ^
    -ldflags="-s -w" ^
    -tags netgo ^
    -o .\out\http2dns.exe .\cmd\http2dns
