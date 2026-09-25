package app

import "context"

// AnalyticsEvent is optional behavioral telemetry, never an operational record.
// Products emit it only after successful persistence; it never changes a command.
type AnalyticsEvent struct {
	Name, UserID, Key, CurationID, Source, Action string
}
type AnalyticsSink interface{ Record(AnalyticsEvent) }
type analyticsContextKey struct{}

func WithAnalyticsSink(ctx context.Context, sink AnalyticsSink) context.Context {
	return context.WithValue(ctx, analyticsContextKey{}, sink)
}
func RecordAnalytics(ctx context.Context, event AnalyticsEvent) {
	if sink, ok := ctx.Value(analyticsContextKey{}).(AnalyticsSink); ok && sink != nil {
		sink.Record(event)
	}
}

// WithoutAnalytics disables optional telemetry for operators and development identities.
func WithoutAnalytics(ctx context.Context) context.Context {
	return WithAnalyticsSink(ctx, nilAnalytics{})
}

type nilAnalytics struct{}

func (nilAnalytics) Record(AnalyticsEvent) {}
