#!/bin/bash

VERSION=${VERSION:-"1.0.0"}
BUILD_TIME=$(date +%Y%m%d_%H%M%S)
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
LDFLAGS="-s -w -X main.Version=$VERSION -X main.BuildTime=$BUILD_TIME -X main.CommitID=$GIT_COMMIT"

MEMORY_MONITOR=${MEMORY_MONITOR:-"true"}
if [ "$MEMORY_MONITOR" = "true" ]; then
    BUILD_TAGS="-tags memory"
    OUTPUT_SUFFIX="_memory"
else
    BUILD_TAGS=""
    OUTPUT_SUFFIX=""
fi

GOOS=${GOOS:-$(go env GOOS)}
GOARCH=${GOARCH:-$(go env GOARCH)}

OUTPUT_NAME="nssh_client"
if [ "$GOOS" = "windows" ]; then
    OUTPUT_NAME="${OUTPUT_NAME}_${GOOS}_${GOARCH}_v${VERSION}.exe"
else
    OUTPUT_NAME="${OUTPUT_NAME}_${GOOS}_${GOARCH}_v${VERSION}"
fi

STANDARD_OUTPUT="${OUTPUT_NAME}"
MINIMAL_OUTPUT="${OUTPUT_NAME}"

echo "========================================"
echo "SSH Reverse Proxy Client - Build Script"
echo "========================================"
echo "Version: $VERSION"
echo "Build Time: $BUILD_TIME"
echo "Platform: $GOOS/$GOARCH"
echo "Memory Monitor: $MEMORY_MONITOR"
echo "========================================"
echo ""

echo "Building standard version..."
CGO_ENABLED=0 GOOS=$GOOS GOARCH=$GOARCH \
    go build \
    $BUILD_TAGS \
    -ldflags="$LDFLAGS" \
    -trimpath \
    -o "$STANDARD_OUTPUT" \
    main.go

if [ $? -ne 0 ]; then
    echo "Build failed"
    exit 1
fi

chmod +x "$STANDARD_OUTPUT"

STANDARD_SIZE=$(ls -lh "$STANDARD_OUTPUT" | awk '{print $5}')
echo "✓ Standard version: $STANDARD_OUTPUT ($STANDARD_SIZE)"
echo ""

if command -v upx &> /dev/null && [ "$GOOS" != "darwin" ]; then
    echo "Building minimal version with UPX..."
    
    ORIGINAL_SIZE=$(stat -f%z "$STANDARD_OUTPUT" 2>/dev/null || stat -c%s "$STANDARD_OUTPUT" 2>/dev/null)
    
    upx --best --lzma "$STANDARD_OUTPUT" 2>/dev/null
    
    if [ $? -eq 0 ]; then
        MINIMAL_SIZE=$(ls -lh "$STANDARD_OUTPUT" | awk '{print $5}')
        echo "✓ Minimal version: $STANDARD_OUTPUT ($MINIMAL_SIZE)"
        
        COMPRESSED_SIZE=$(stat -f%z "$STANDARD_OUTPUT" 2>/dev/null || stat -c%s "$STANDARD_OUTPUT" 2>/dev/null)
        COMPRESSION_RATIO=$(echo "scale=1; 100 * (1 - $COMPRESSED_SIZE / $ORIGINAL_SIZE)" | bc 2>/dev/null || echo "N/A")
        if [ "$COMPRESSION_RATIO" != "N/A" ]; then
            echo "  Compression: ${COMPRESSION_RATIO}%"
        fi
    else
        echo "✗ UPX compression failed"
    fi
else
    echo "⊘ UPX not available or not supported on $GOOS, skipping minimal version"
fi

echo ""
echo "========================================"
echo "Build completed!"
echo "========================================"
echo "Output file:"
echo "  $STANDARD_OUTPUT ($(ls -lh "$STANDARD_OUTPUT" | awk '{print $5}'))"
echo "========================================"
