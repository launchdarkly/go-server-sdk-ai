# LaunchDarkly Server-side AI SDK for Go

[![Actions Status](https://github.com/launchdarkly/go-server-sdk-ai/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/launchdarkly/go-server-sdk-ai/actions/workflows/ci.yml)

> [!CAUTION]
> This AI SDK is in pre-release and not subject to backwards compatibility guarantees. The API may change based on feedback.
>
> Pin to a specific minor version and review the [release notes](https://github.com/launchdarkly/go-server-sdk-ai/releases) before upgrading.
>
> Active feature development is ongoing in the [Python][python-ai-sdk] and [Node.js][node-ai-sdk] AI SDKs, so this SDK will receive new features at a slower pace. Refer to those for the latest capabilities.

[python-ai-sdk]: https://github.com/launchdarkly/python-server-sdk-ai
[node-ai-sdk]: https://github.com/launchdarkly/js-core/tree/main/packages/sdk/server-ai

## LaunchDarkly overview

[LaunchDarkly](https://www.launchdarkly.com) is a feature management platform that serves trillions of feature flags daily to help teams build better software, faster. [Get started](https://docs.launchdarkly.com/home/getting-started) using LaunchDarkly today!

[![Twitter Follow](https://img.shields.io/twitter/follow/launchdarkly.svg?style=social&label=Follow&maxAge=2592000)](https://twitter.com/intent/follow?screen_name=launchdarkly)

## Getting started

This module provides the `ldai` package, built on top of the base [`go-server-sdk`](https://github.com/launchdarkly/go-server-sdk). It resolves AI model configuration (model, provider, prompt messages) from LaunchDarkly AI Configs, interpolates prompt templates, and reports generation metrics (tokens, latency, success/error, feedback) back to LaunchDarkly.

Import the base Server SDK and this AI SDK:

```go
import (
	ld "github.com/launchdarkly/go-server-sdk/v7"
	"github.com/launchdarkly/go-sdk-common/v3/ldcontext"
	"github.com/launchdarkly/go-server-sdk-ai/ldai"
)
```

Configure the base LaunchDarkly Server SDK:

```go
sdkClient, _ := ld.MakeClient("your-sdk-key", 5*time.Second)
```

Instantiate the AI client, passing in the base Server SDK:

```go
aiClient, err := ldai.NewClient(sdkClient)
```

Fetch a model configuration for a specific LaunchDarkly context:

```go
// The default value 'ldai.Disabled()' is returned if LaunchDarkly is unavailable or the
// config cannot be fetched. To customize the default value, use ldai.NewConfig().
config := aiClient.CompletionConfig("your-config-key", ldcontext.New("user-key"), ldai.Disabled(), nil)

// Access the resolved configuration via methods on config (config.Messages(),
// config.ModelName(), config.ProviderName(), and so on). To generate analytic events for
// usage of the model config, obtain a tracker by calling config.CreateTracker().
tracker := config.CreateTracker()
```

## Learn more

Read our [documentation](https://docs.launchdarkly.com) for in-depth instructions on configuring and using LaunchDarkly. You can also head straight to the [complete reference guide for this SDK](https://docs.launchdarkly.com/sdk/ai/go).

## Contributing

We encourage pull requests and other contributions from the community. Check out our [contributing guidelines](CONTRIBUTING.md) for instructions on how to contribute to this library.

## About LaunchDarkly

* LaunchDarkly is a continuous delivery platform that provides feature flags as a service and allows developers to iterate quickly and safely. We allow you to easily flag your features and manage them from the LaunchDarkly dashboard. With LaunchDarkly, you can:
    * Roll out a new feature to a subset of your users (like a group of users who opt-in to a beta tester group), gathering feedback and bug reports from real-world use cases.
    * Gradually roll out a feature to an increasing percentage of users, and track the effect that the feature has on key metrics (for instance, how likely is a user to complete a purchase if they have feature A versus feature B?).
    * Turn off a feature that you realize is causing performance problems in production, without needing to re-deploy, or even restart the application with a changed configuration file.
    * Grant access to certain features based on user attributes, like payment plan (eg: users on the 'gold' plan get access to more features than users in the 'silver' plan). Disable parts of your application to facilitate maintenance, without taking everything offline.
* LaunchDarkly provides feature flag SDKs for a wide variety of languages and technologies. Read [our documentation](https://docs.launchdarkly.com/sdk) for a complete list.
* Explore LaunchDarkly
    * [launchdarkly.com](https://www.launchdarkly.com/ "LaunchDarkly Main Website") for more information
    * [docs.launchdarkly.com](https://docs.launchdarkly.com/ "LaunchDarkly Documentation") for our documentation and SDK reference guides
    * [apidocs.launchdarkly.com](https://apidocs.launchdarkly.com/ "LaunchDarkly API Documentation") for our API documentation
    * [blog.launchdarkly.com](https://blog.launchdarkly.com/ "LaunchDarkly Blog Documentation") for the latest product updates
