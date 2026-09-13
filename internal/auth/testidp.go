//go:build !ghs_testidp

package auth

// TestIdentityProviderCompiled reports whether the test identity provider —
// which lets the local demo sign in as a sample account without talking to
// GitHub at all — is present in this binary.
//
// It is always false here. This file is what an ordinary `go build` links;
// the actual implementation lives in testidp_enabled.go, gated behind the
// ghs_testidp build tag, so a normal release artifact does not even contain
// the code that could satisfy this shortcut — there is nothing to
// misconfigure at runtime because the capability was never compiled in.
func TestIdentityProviderCompiled() bool { return false }
