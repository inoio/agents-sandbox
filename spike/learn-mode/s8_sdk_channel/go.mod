// Throwaway module for the S1 SDK channel-semantics spike. It lives in its own
// module so it stays out of the repository's build, lint and compile-all gates
// (spike/learn-mode is inside the root module otherwise). Deleted once the S1
// decisions land in the learn-mode subspecs.
module spike/s8_sdk_channel

go 1.27.0

require github.com/superradcompany/microsandbox/sdk/go v0.7.15
