package tracing

import (
	"net/http"
)

func HTTPMiddleware(spanName string, recorder SpanRecorder) func(http.Handler) http.Handler {
	return HTTPMiddlewareWithOptions(spanName, recorder, SpanOptions{})
}

func HTTPMiddlewareWithOptions(spanName string, recorder SpanRecorder, opts SpanOptions) func(http.Handler) http.Handler {
	if spanName == "" {
		spanName = "http.request"
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			if header := r.Header.Get("traceparent"); header != "" {
				if extracted, ok := ExtractTraceParent(ctx, header); ok {
					ctx = extracted
				}
			}
			spanCtx, finish := StartSpanWithOptions(ctx, spanName, recorder, opts)
			if traceparent, ok := FormatTraceParent(spanCtx); ok {
				w.Header().Set("traceparent", traceparent)
			}
			next.ServeHTTP(w, r.WithContext(spanCtx))
			finish(nil)
		})
	}
}
