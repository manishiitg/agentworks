package workspace

import "context"

// FileReadReceipt and BeforeFileUpdate are installed by the workflow server.
// A policy must return a release function held until the write completes.
// Standalone workspace clients retain their existing behavior.
var FileReadReceipt func(context.Context, string, string)
var BeforeFileUpdate func(context.Context, string, string) (func(), error)
var AdditionalPathPolicy func(context.Context, string, bool) error

var FileWriteReceipt func(context.Context, string, string)

// FileWriteInterceptor stages native multi-file edits; handled=false preserves
// ordinary clients. FileReadOverlay supplies their own uncommitted contents.
var FileWriteInterceptor func(context.Context, string, string) (bool, error)
var FileReadOverlay func(context.Context, string) (string, bool, bool)
