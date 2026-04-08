#!/bin/bash

IMAGE_TAG=letstool/http2dns:latest

docker build \
	-t "$IMAGE_TAG" \
       -f build/Dockerfile \
       .
