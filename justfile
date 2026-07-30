# SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
# SPDX-License-Identifier: MIT OR Apache-2.0

set shell := ["pwsh", "-NoLogo", "-NoProfile", "-Command"]

default:
    @just --list

setup:
    mise install
    lefthook install

format:
    gofmt -w .
    taplo format

format-check:
    $files = @(gofmt -l .); if ($files.Count -ne 0) { $files; throw "gofmt changes required" }
    taplo format --check

generate:
    go generate ./...

generated-check:
    go generate ./...
    git diff --exit-code

lint:
    golangci-lint run ./...
    actionlint
    typos

test:
    go test ./...

integration:
    go test -tags=integration ./internal/platform

coverage:
    go test -covermode atomic -coverprofile coverage.out ./...
    go tool cover -func=coverage.out

security:
    govulncheck ./...
    gitleaks dir .

reuse:
    reuse lint

build:
    $env:CGO_ENABLED = "0"; go build -trimpath ./cmd/startclean
    $env:CGO_ENABLED = "0"; $env:GOARCH = "arm64"; go build -trimpath ./cmd/startclean

release-check:
    goreleaser check
    goreleaser release --snapshot --clean

ci: format-check generated-check lint test integration coverage security reuse build release-check
