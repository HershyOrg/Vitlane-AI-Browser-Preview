package app

import "context"

type transactionMarker struct{}

// MarkTransaction is installed by a transaction adapter, never an HTTP caller.
func MarkTransaction(ctx context.Context) context.Context {
	return context.WithValue(ctx, transactionMarker{}, true)
}
func InTransaction(ctx context.Context) bool { v, _ := ctx.Value(transactionMarker{}).(bool); return v }
