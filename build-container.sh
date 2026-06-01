#!/bin/bash

# Configuration
IMAGE_NAME=${CONTAINER_NAME:-"sys-agent-workspace"}
RUNTIME=${CONTAINER_RUNTIME:-"podman"}

echo "Building workspace container image: $IMAGE_NAME:latest using $RUNTIME..."

$RUNTIME build -t "$IMAGE_NAME:latest" -f Containerfile .

if [ $? -eq 0 ]; then
    echo "Successfully built $IMAGE_NAME:latest"
else
    echo "Failed to build container image"
    exit 1
fi
