# Contributing to the LaunchDarkly Server-side AI SDK for Go

LaunchDarkly has published an [SDK contributor's guide](https://docs.launchdarkly.com/sdk/concepts/contributors-guide) that provides a detailed explanation of how our SDKs work. See below for additional information on how to contribute to this SDK.

## Submitting bug reports and feature requests

The LaunchDarkly SDK team monitors the [issue tracker](https://github.com/launchdarkly/go-server-sdk-ai/issues) in the SDK repository. Bug reports and feature requests specific to this library should be filed in this issue tracker. The SDK team will respond to all newly filed issues within two business days.

## Submitting pull requests

We encourage pull requests and other contributions from the community. Before submitting pull requests, ensure that all temporary or unintended code is removed. Don't worry about adding reviewers to the pull request; the LaunchDarkly SDK team will add themselves. The SDK team will acknowledge all pull requests within two business days.

## Build instructions

### Prerequisites

This project uses [Go modules](https://go.dev/wiki/Modules). You will need a supported version of Go installed; see the `go` directive in `go.mod` for the minimum supported version.

### Building

```shell
go build ./...
```

### Testing

To run all unit tests:

```shell
go test ./...
```

To run with the race detector and coverage as CI does:

```shell
go test -race -coverprofile=coverage.out ./...
```

### Running the linter

[golangci-lint](https://golangci-lint.run/) is used in CI to catch potential problems. To run it locally:

```shell
golangci-lint run
```

## Code organization

The core SDK lives in the `ldai` package. Provider integrations (e.g. OpenAI) are published as separate sub-modules, each with its own `go.mod`, so that consumers only pull in the dependencies they actually use.
