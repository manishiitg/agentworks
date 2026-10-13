// Package vaultruntime defines the compile-time contract for a trusted Vault service runtime.
package vaultruntime

import api "github.com/manishiitg/agentworks/packages/server-product-sdk"

const Version = 1

// Factory owns the existing Vault listener and stores. Products are separately
// compiled SDK factories mounted by this runtime; no credential is carried here.
// Runtime selection is operator-only and must never fall back after private fails.
type Factory struct {
	Version int
	Run     func([]api.Factory) error
}
