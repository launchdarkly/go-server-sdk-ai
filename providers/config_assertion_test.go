package providers_test

import (
	"github.com/launchdarkly/go-server-sdk-ai/ldai"
	"github.com/launchdarkly/go-server-sdk-ai/providers"
)

// Compile-time assertion that *ldai.Config satisfies providers.Config. This lives in an external
// test package: the providers package must not import ldai (so ldai can later depend on
// providers without a cycle), but the contract between them still needs to be enforced.
var _ providers.Config = (*ldai.Config)(nil)
