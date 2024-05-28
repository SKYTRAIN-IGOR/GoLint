#!/bin/bash -e

# Benchmark with a local version
# Usage: ./scripts/bench/bench_local.sh gosec v1.59.0

# ex: gosec
LINTER=$1

# ex: v1.59.0
VERSION=$2

## Clean

function cleanBinaries() {
  echo "Clean binaries"
  rm "./golangci-lint-${VERSION}"
  rm ./golangci-lint
}

trap cleanBinaries EXIT

## Download version

curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b "./temp-${VERSION}" "${VERSION}"

mv "temp-${VERSION}/golangci-lint" "./golangci-lint-${VERSION}"
rm -rf "temp-${VERSION}"

## Build local version

make build

## Run

hyperfine \
--prepare './golangci-lint cache clean' "./golangci-lint run --issues-exit-code 0 --print-issued-lines=false --enable-only ${LINTER}" \
--prepare "./golangci-lint-${VERSION} cache clean" "./golangci-lint-${VERSION} run --issues-exit-code 0 --print-issued-lines=false --enable-only ${LINTER}"

