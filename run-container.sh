#!/bin/bash
# Workspace Container Manager
# Starts and manages the workspace container for the autonomous agent.

set -e

# Configuration (can be overridden via environment)
CONTAINER_NAME="${CONTAINER_NAME:-sys-agent-workspace}"
CONTAINER_RUNTIME="${CONTAINER_RUNTIME:-podman}"
IMAGE_NAME="${IMAGE_NAME:-sys-agent-workspace:latest}"
WORKSPACE_DIR="${WORKSPACE_DIR:-$(pwd)/workspace}"

echo "=== Workspace Container Manager ==="
echo ""
echo "Container:  $CONTAINER_NAME"
echo "Runtime:    $CONTAINER_RUNTIME"
echo "Image:      $IMAGE_NAME"
echo "Workspace:  $WORKSPACE_DIR"
echo ""

# Create workspace directory if it doesn't exist
if [ ! -d "$WORKSPACE_DIR" ]; then
    echo "Creating workspace directory: $WORKSPACE_DIR"
    mkdir -p "$WORKSPACE_DIR"
fi

# Check if container is already running
if $CONTAINER_RUNTIME ps --format "{{.Names}}" | grep -q "^${CONTAINER_NAME}$"; then
    echo "Container '$CONTAINER_NAME' is already running."
    echo ""
    echo "Useful commands:"
    echo "  Exec:  $CONTAINER_RUNTIME exec -it $CONTAINER_NAME bash"
    echo "  Stop:  $CONTAINER_RUNTIME stop $CONTAINER_NAME"
    echo "  Logs:  $CONTAINER_RUNTIME logs $CONTAINER_NAME"
    exit 0
fi

# Check if container exists but is stopped
if $CONTAINER_RUNTIME ps -a --format "{{.Names}}" | grep -q "^${CONTAINER_NAME}$"; then
    echo "Starting stopped container '$CONTAINER_NAME'..."
    $CONTAINER_RUNTIME start "$CONTAINER_NAME"
    echo "Container started."
    exit 0
fi

# Container doesn't exist, create it
echo "Creating new container '$CONTAINER_NAME'..."
$CONTAINER_RUNTIME run -d \
    --name "$CONTAINER_NAME" \
    -v "${WORKSPACE_DIR}:/workspace" \
    --network=host \
    -i \
    "$IMAGE_NAME"

echo ""
echo "Container created and running!"
echo ""
echo "Useful commands:"
echo "  Exec:    $CONTAINER_RUNTIME exec -it $CONTAINER_NAME bash"
echo "  Stop:    $CONTAINER_RUNTIME stop $CONTAINER_NAME"
echo "  Remove:  $CONTAINER_RUNTIME rm -f $CONTAINER_NAME"
echo ""
