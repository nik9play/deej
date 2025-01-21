#!/bin/sh

# Check if mode parameter is provided
if [ -z "$1" ]; then
    echo "Usage: $0 [dev|release]"
    exit 1
fi

MODE=$1

# Validate the mode parameter
if [ "$MODE" != "dev" ] && [ "$MODE" != "release" ]; then
    echo "Invalid mode: $MODE"
    echo "Use 'dev' or 'release'."
    exit 1
fi

echo "Building deej ($MODE)..."

# Get git commit and version tag
GIT_COMMIT=$(git rev-list -1 --abbrev-commit HEAD)
VERSION_TAG=$(git describe --tags --always)
BUILD_TYPE=$MODE

echo 'Embedding build-time parameters:'
echo "- gitCommit $GIT_COMMIT"
echo "- versionTag $VERSION_TAG"
echo "- buildType $BUILD_TYPE"

# Build based on mode
if [ "$MODE" = "dev" ]; then
    go build -o build/deej-dev -ldflags "-X main.gitCommit=$GIT_COMMIT -X main.versionTag=$VERSION_TAG -X main.buildType=$BUILD_TYPE" ./pkg/deej/cmd
else
    go build -o build/deej-release -ldflags "-s -w -X main.gitCommit=$GIT_COMMIT -X main.versionTag=$VERSION_TAG -X main.buildType=$BUILD_TYPE" ./pkg/deej/cmd
fi

# Check if build succeeded
if [ $? -eq 0 ]; then
    echo "Done."
else
    echo 'Error: "go build" exited with a non-zero code. Are you running this script from the root deej directory?'
    exit 1
fi
