# SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
# SPDX-License-Identifier: MIT OR Apache-2.0

set windows-shell := ["pwsh", "-NoLogo", "-NoProfile", "-Command"]

default:
    @just --list

setup:
    mise install
    lefthook install

format:
    go run ./cmd/devtool format

format-check:
    go run ./cmd/devtool format --check

generate:
    go run ./cmd/devtool generate

generated-check:
    go run ./cmd/devtool generate --check

lint:
    go run ./cmd/devtool lint

vet:
    go run ./cmd/devtool vet

test:
    go run ./cmd/devtool test

integration:
    go run ./cmd/devtool integration

stress:
    go run ./cmd/devtool stress

coverage:
    go run ./cmd/devtool coverage

module-verify:
    go run ./cmd/devtool module-verify

race:
    go run ./cmd/devtool race

fuzz:
    go run ./cmd/devtool fuzz

security:
    go run ./cmd/devtool security

secrets:
    go run ./cmd/devtool secrets

reuse:
    go run ./cmd/devtool reuse

build:
    go run ./cmd/devtool build

release-source:
    go run ./cmd/devtool release-source

release-check:
    go run ./cmd/devtool release-check

release-smoke:
    go run ./cmd/devtool release-smoke

ci:
    go run ./cmd/devtool ci
