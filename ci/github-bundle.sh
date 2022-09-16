#!/bin/bash

# Exit on failure
set -e

echo "GITHUB_REF_NAME = $GITHUB_REF_NAME"
echo "MATRIX_OS = $MATRIX_OS"
echo "PROGRAM = $PROGRAM"

if [ "$MATRIX_OS" = "ubuntu-latest" ]; then
    TARGET="linux"
elif [ "$MATRIX_OS" = "macos-latest" ]; then
    TARGET="macos"
elif [ "$MATRIX_OS" = "windows-latest" ]; then
    TARGET="windows"
else
    echo "ERROR: Unsupported OS, $MATRIX_OS"
    exit 1
fi

if [ "$GITHUB_REF_NAME" = "main" ]; then
    TAG="latest"
else
    TAG=$GITHUB_REF_NAME
fi

if [ "$TARGET" = "windows" ]; then
    BINARY=${PROGRAM}.exe
else
    BINARY=${PROGRAM}
fi

PAYLOAD="${BINARY} README.md LICENSE.md assets/ config/"
ZIPFILE="${PROGRAM}_${TAG}_${TARGET}.zip"

echo "ZIPFILE = $ZIPFILE"
echo "PAYLOAD = $PAYLOAD"

mkdir upload
#zip -r upload/$ZIPFILE $PAYLOAD
7z a upload/$ZIPFILE $PAYLOAD
