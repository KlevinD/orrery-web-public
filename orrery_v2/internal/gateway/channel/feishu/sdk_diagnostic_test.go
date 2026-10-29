package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSDKDiagnosticClassifiesWithoutLeakingPayload(t *testing.T) {
	fields := feishuSDKDiagnostic("sdk_error", []interface{}{errors.New("dial tcp timeout status=503 token=secret-value body=private-message")})
	if fields["error_kind"] != "timeout" || fields["sdk_status_code"] != "503" {
		t.Fatalf("fields=%v", fields)
	}
	raw, _ := json.Marshal(fields)
	if strings.Contains(string(raw), "secret-value") || strings.Contains(string(raw), "private-message") {
		t.Fatal("SDK payload leaked")
	}
}

type sdkDiagnosticLog struct {
	infos, errors int
	fields        map[string]any
}

func (l *sdkDiagnosticLog) Info(_ string, fields map[string]any)  { l.infos++; l.fields = fields }
func (l *sdkDiagnosticLog) Warn(_ string, fields map[string]any)  { l.fields = fields }
func (l *sdkDiagnosticLog) Error(_ string, fields map[string]any) { l.errors++; l.fields = fields }

func TestSDKShutdownLoggingKeepsUnexpectedFailuresVisible(t *testing.T) {
	for _, test := range []struct {
		name, message    string
		cancel, deadline bool
		wantErrors       int
	}{
		{"normal shutdown", "connection is closed, receive message loop exit", true, false, 0},
		{"live disconnect", "connection is closed, receive message loop exit", false, false, 1},
		{"deadline is a failure", "connection is closed", false, true, 1},
		{"panic during shutdown", "receive message loop panic: nil pointer", true, false, 1},
		{"panic mentioning EOF", "receive message loop panic: unexpected EOF", true, false, 1},
		{"authentication during shutdown", "authentication failed code=401", true, false, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if test.cancel {
				cancel()
			}
			if test.deadline {
				var done context.CancelFunc
				ctx, done = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				defer done()
			}
			log := &sdkDiagnosticLog{}
			feishuSDKLogger{logger: log, lifecycle: ctx}.Error(context.Background(), errors.New(test.message))
			if log.errors != test.wantErrors || log.infos != 1-test.wantErrors {
				t.Fatalf("errors=%d infos=%d", log.errors, log.infos)
			}
		})
	}
}

func TestSDKTransportDiagnosticsRetainCauseWithoutSecrets(t *testing.T) {
	fields := feishuSDKDiagnostic("sdk_error", []interface{}{"websocket: close 1006 wss://name:credential@socket.feishu.cn/path?device_id=private&token=private token=private body=customer-content"})
	raw, _ := json.Marshal(fields)
	if fields["error_code"] != "sdk_error_websocket" || !strings.Contains(fields["error_cause"].(string), "close 1006") {
		t.Fatalf("transport diagnosis missing: %v", fields)
	}
	for _, private := range []string{"credential", "private", "customer-content", "/path"} {
		if strings.Contains(string(raw), private) {
			t.Fatalf("leaked %q: %s", private, raw)
		}
	}
	unknown := feishuSDKDiagnostic("sdk_error", []interface{}{"handle message failed customer-content"})
	if unknown["error_cause"] != nil {
		t.Fatal("unclassified event payload retained")
	}
}
