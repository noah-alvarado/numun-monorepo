package observability

import (
	"context"

	"connectrpc.com/connect"
	"github.com/getsentry/sentry-go"
)

// ConnectInterceptor reports server-side RPC failures to Sentry. Client-caused
// codes (invalid_argument, not_found, permission_denied, …) are expected and
// skipped.
func ConnectInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			resp, err := next(ctx, req)
			if err == nil || sentry.CurrentHub().Client() == nil {
				return resp, err
			}
			switch connect.CodeOf(err) {
			case connect.CodeInternal, connect.CodeUnknown, connect.CodeDataLoss:
			default:
				return resp, err
			}
			hub := sentry.GetHubFromContext(ctx)
			if hub == nil {
				hub = sentry.CurrentHub().Clone()
			}
			hub.WithScope(func(scope *sentry.Scope) {
				scope.SetTag("rpc.procedure", req.Spec().Procedure)
				hub.CaptureException(err)
			})
			// Lambda freezes after the response, so send before returning.
			hub.Flush(FlushTimeout)
			return resp, err
		}
	}
}
