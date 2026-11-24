package observability

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/getsentry/sentry-go"
)

type recordingTransport struct {
	mu     sync.Mutex
	events []*sentry.Event
}

func (t *recordingTransport) Configure(sentry.ClientOptions)        {}
func (t *recordingTransport) Flush(time.Duration) bool              { return true }
func (t *recordingTransport) FlushWithContext(context.Context) bool { return true }
func (t *recordingTransport) Close()                                {}
func (t *recordingTransport) SendEvent(e *sentry.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, e)
}

func TestConnectInterceptorReportsOnlyServerErrors(t *testing.T) {
	tr := &recordingTransport{}
	if err := sentry.Init(sentry.ClientOptions{Dsn: "https://k@o1.ingest.sentry.io/1", Transport: tr}); err != nil {
		t.Fatalf("sentry init: %v", err)
	}
	t.Cleanup(func() { _ = sentry.Init(sentry.ClientOptions{}) })

	call := func(code connect.Code) {
		h := ConnectInterceptor()(func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
			return nil, connect.NewError(code, errors.New("boom"))
		})
		req := connect.NewRequest(&struct{}{})
		_, _ = h(context.Background(), req)
	}
	call(connect.CodeNotFound)
	call(connect.CodeInvalidArgument)
	call(connect.CodeInternal)

	if len(tr.events) != 1 {
		t.Fatalf("want 1 event (internal only), got %d", len(tr.events))
	}
	if fp := tr.events[0].Fingerprint; len(fp) != 2 || fp[0] != "{{ default }}" {
		t.Fatalf("want fingerprint [{{ default }}, <procedure>], got %v", fp)
	}
}
