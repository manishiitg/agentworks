package main

import api "github.com/manishiitg/agentworks/packages/server-product-sdk"

// Public builds have no private products or dependency on the deployments repo.
// The private server build replaces only this composition root via a Go overlay.
func serverProductFactories() ([]api.Factory, error) { return nil, nil }
