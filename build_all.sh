#!/bin/bash

VERSION=${VERSION:-"1.0.0"}
BUILD_TIME=$(date +%Y%m%d_%H%M%S)
TIMESTAMP=$(date +%Y%m%d_%H%M%S)
LDFLAGS="-s -w -X main.Version=$VERSION -X main.BuildTime=$BUILD_TIME"

# 支持的平台和架构
PLATFORMS=(
    "linux/amd64"
    "linux/arm64"
    "linux/386"
    "linux/arm/7"
    "darwin/amd64"
    "darwin/arm64"
    "windows/amd64"
    "windows/386"
)

mkdir -p bin

echo "========================================"
echo "SSH Reverse Proxy Client - Build All"
echo "========================================"
echo "Version: $VERSION"
echo "Build Time: $BUILD_TIME"
echo "========================================"
echo ""

TOTAL=0
SUCCESS=0
FAILED=0

for platform in "${PLATFORMS[@]}"; do
    IFS='/' read -r GOOS GOARCH GOARM <<< "$platform"
    
    TOTAL=$((TOTAL + 1))
    
    # 构建文件名
    OUTPUT_NAME="${TIMESTAMP}_${GOOS}_${GOARCH}_nssh_client"
    if [ "$GOOS" = "windows" ]; then
        OUTPUT_NAME="${OUTPUT_NAME}.exe"
    fi
    
    STANDARD_OUTPUT="bin/${OUTPUT_NAME}"
    MINIMAL_OUTPUT="bin/${TIMESTAMP}_${GOOS}_${GOARCH}_minimal_nssh_client"
    if [ "$GOOS" = "windows" ]; then
        MINIMAL_OUTPUT="${MINIMAL_OUTPUT}.exe"
    fi
    
    echo "[$TOTAL/${#PLATFORMS[@]}] Building ${GOOS}/${GOARCH}..."
    
    # 编译标准版本
    if [ -n "$GOARM" ]; then
        GOARM=$GOARM CGO_ENABLED=0 GOOS=$GOOS GOARCH=$GOARCH \
            go build \
            -ldflags="$LDFLAGS" \
            -trimpath \
            -o "$STANDARD_OUTPUT" \
            main.go 2>/dev/null
    else
        CGO_ENABLED=0 GOOS=$GOOS GOARCH=$GOARCH \
            go build \
            -ldflags="$LDFLAGS" \
            -trimpath \
            -o "$STANDARD_OUTPUT" \
            main.go 2>/dev/null
    fi
    
    if [ $? -ne 0 ]; then
        echo "  ✗ Build failed"
        FAILED=$((FAILED + 1))
        continue
    fi
    
    STANDARD_SIZE=$(ls -lh "$STANDARD_OUTPUT" | awk '{print $5}')
    echo "  ✓ Standard: $(basename $STANDARD_OUTPUT) ($STANDARD_SIZE)"
    
    # UPX压缩（非macOS平台）
    if [ "$GOOS" != "darwin" ] && command -v upx &> /dev/null; then
        cp "$STANDARD_OUTPUT" "$MINIMAL_OUTPUT"
        upx --best --lzma "$MINIMAL_OUTPUT" 2>/dev/null
        
        if [ $? -eq 0 ]; then
            MINIMAL_SIZE=$(ls -lh "$MINIMAL_OUTPUT" | awk '{print $5}')
            echo "  ✓ Minimal:  $(basename $MINIMAL_OUTPUT) ($MINIMAL_SIZE)"
            
            # 计算压缩率
            if [ "$GOOS" = "linux" ]; then
                COMPRESSION_RATIO=$(echo "scale=1; 100 * (1 - $(stat -f%z "$MINIMAL_OUTPUT") / $(stat -f%z "$STANDARD_OUTPUT"))" | bc 2>/dev/null || echo "N/A")
            else
                COMPRESSION_RATIO="N/A"
            fi
            
            if [ "$COMPRESSION_RATIO" != "N/A" ]; then
                echo "    Compression: ${COMPRESSION_RATIO}%"
            fi
        else
            rm -f "$MINIMAL_OUTPUT"
        fi
    fi
    
    SUCCESS=$((SUCCESS + 1))
    echo ""
done

echo "========================================"
echo "Build Summary"
echo "========================================"
echo "Total:   $TOTAL"
echo "Success: $SUCCESS"
echo "Failed:  $FAILED"
echo "========================================"
echo ""

if [ $SUCCESS -gt 0 ]; then
    echo "Output files in bin/:"
    ls -lh bin/ | grep -v "^total" | grep -v "^d" | awk '{print "  " $9 " (" $5 ")"}'
fi

echo ""
echo "========================================"
