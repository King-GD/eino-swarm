package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestStreamReadTimeoutDuringAnIncompleteResponsesFrame(t *testing.T) {
	tail := `data: {"type":"response.output_text.delta","delta":"private`
	ctx := context.Background()
	completion := &streamCompletion{}
	completion.http.Store(true)
	cause := &net.OpError{Op: "read", Net: "tcp", Err: os.ErrDeadlineExceeded}
	resp := &http.Response{Body: &streamCompletionBody{
		ReadCloser: io.NopCloser(io.MultiReader(strings.NewReader(tail), iotest.ErrReader(cause))),
		ctx:        ctx,
		completion: completion,
	}}
	err := readResponsesStream(resp, &responsesStreamState{})
	var timeout *StreamReadTimeoutError
	if !errors.As(err, &timeout) || !errors.Is(err, cause) {
		t.Fatalf("incomplete frame masked transport timeout: %v", err)
	}
	trace := completion.traceError(ctx, err)
	if !errors.Is(trace, cause) || !strings.Contains(trace.Error(), fmt.Sprintf("received_bytes=%d ", len(tail))) ||
		strings.Contains(trace.Error(), "private") {
		t.Fatalf("incomplete frame trace: %v", trace)
	}
}

func TestStreamReadTraceWithoutBodyBytes(t *testing.T) {
	ctx := context.Background()
	const id = "98e138fe30b7402494e7c85604ccdcbe"
	headers := http.Header{}
	headers.Set("X-Request-ID", id)
	completion := &streamCompletion{streamReadTrace: streamReadTrace{requestID: streamRequestID(headers)}}
	completion.http.Store(true)
	cause := errors.New("transport detail with private address and credential")
	err := completion.observeRead(ctx, 0, cause)
	trace := completion.traceError(ctx, err)
	if trace.Error() != "provider: model response body read failed request_id="+id+" received_bytes=0" || !errors.Is(trace, cause) {
		t.Fatalf("zero-byte read error: %v", trace)
	}
	headers.Add("X-Request-ID", id)
	if id := streamRequestID(headers); id != "" {
		t.Fatalf("accepted ambiguous request ID: %s", id)
	}
	headers.Set("X-Request-ID", strings.Repeat("z", 32))
	if id := streamRequestID(headers); id != "" {
		t.Fatalf("accepted non-hex request ID: %s", id)
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		err := completion.observeRead(ctx, 0, cause)
		if err != cause || completion.traceError(ctx, err) != cause {
			t.Fatalf("context error became a body read failure: %v", err)
		}
	}
}

func TestStreamReadDiagnosticsOverHTTP2(t *testing.T) {
	const id = "98e138fe30b7402494e7c85604ccdcbe"
	for _, wire := range []struct{ mode, delta, terminal string }{
		{"responses", `{"type":"response.output_text.delta","delta":"private partial"}`, `{"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"private partial"}]}]}}`},
		{"chat", `{"choices":[{"index":0,"delta":{"role":"assistant","content":"private partial"}}]}`, "[DONE]"},
	} {
		for _, tc := range []struct{ name, header, want string }{
			{"read timeout", id, "model response read timed out"},
			{"truncated", "Bearer private-credential", "unexpected EOF"},
			{"idle", "", "stream idle timeout"},
			{"cancel", id, "context canceled"},
			{"finished", id, ""},
		} {
			t.Run(wire.mode+"/"+tc.name, func(t *testing.T) {
				body := "data: " + wire.delta + "\n\n"
				srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Proto != "HTTP/2.0" {
						t.Errorf("model HTTP protocol = %s", r.Proto)
					}
					w.Header().Set("Content-Type", "text/event-stream")
					w.Header().Set("X-Request-ID", tc.header)
					if _, err := io.WriteString(w, body); err != nil {
						t.Error(err)
						return
					}
					w.(http.Flusher).Flush()
					switch tc.name {
					case "finished":
						if _, err := fmt.Fprintf(w, "data: %s\n\n", wire.terminal); err != nil {
							t.Error(err)
						}
					case "truncated":
					default:
						<-r.Context().Done()
					}
				}))
				srv.EnableHTTP2 = true
				srv.StartTLS()
				defer srv.Close()

				transport := srv.Client().Transport.(*http.Transport)
				connections := make(chan net.Conn, 1)
				transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
					conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
					if err == nil {
						connections <- conn
					}
					return conn, err
				}
				idle := 10 * time.Second
				if tc.name == "idle" {
					idle = 300 * time.Millisecond
				}
				client := &http.Client{Transport: &idleTransport{base: transport, idle: idle}}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				p := config.Provider{BaseURL: srv.URL, Model: "fixture"}
				var inner model.BaseChatModel
				var err error
				if wire.mode == "responses" {
					inner, err = newResponsesModelWithClient(p, client)
				} else {
					inner, err = openaimodel.NewChatModel(ctx, &openaimodel.ChatModelConfig{BaseURL: p.BaseURL, Model: p.Model, HTTPClient: client})
				}
				if err != nil {
					t.Fatal(err)
				}
				calls := make(chan CallRecord, 2)
				m := wrap(inner, "manager", p, "", 0, func(call CallRecord) { calls <- call })
				stream, err := m.Stream(ctx, []*schema.Message{schema.UserMessage("private prompt")})
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				first, err := stream.Recv()
				if err != nil || first.Content != "private partial" {
					t.Fatalf("first response=%+v, error=%v", first, err)
				}
				conn := <-connections
				if tc.name == "read timeout" {
					if err := conn.SetReadDeadline(time.Now()); err != nil {
						t.Fatal(err)
					}
				}
				if tc.name == "cancel" {
					cancel()
				}
				for err == nil {
					_, err = stream.Recv()
				}
				call := <-calls
				if call.OutputChars != len(first.Content) || len(calls) != 0 {
					t.Fatalf("call=%+v additional calls=%d", call, len(calls))
				}
				if tc.name == "finished" {
					if !errors.Is(err, io.EOF) || call.Err != nil {
						t.Fatalf("finished stream: terminal=%v trace=%v", err, call.Err)
					}
					return
				}
				if !strings.Contains(err.Error(), tc.want) || call.Err == nil {
					t.Fatalf("terminal=%v trace=%v, want %q", err, call.Err, tc.want)
				}
				if (tc.name != "cancel") != isBackoffFailure(err) {
					t.Fatalf("stream failure changed the backoff decision: %v", err)
				}
				trace := call.Err.Error()
				if tc.name == "cancel" {
					if !errors.Is(err, context.Canceled) || strings.Contains(trace, "received_bytes") {
						t.Fatalf("cancel was reported as a transport failure: %v / %v", err, trace)
					}
					return
				}
				if strings.Contains(err.Error(), "received_bytes") || !strings.Contains(trace, fmt.Sprintf("received_bytes=%d ", len(body))) {
					t.Fatalf("read size was missing or public: terminal=%v trace=%v", err, trace)
				}
				_, stamp, found := strings.Cut(trace, "last_received_at=")
				lastRead, parseErr := time.Parse(time.RFC3339Nano, stamp)
				if !found || parseErr != nil || lastRead.After(time.Now()) || time.Since(lastRead) > 5*time.Second {
					t.Fatalf("missing last read timestamp: %s", trace)
				}
				if strings.Contains(trace, "request_id="+id) != (tc.header == id) ||
					strings.Contains(trace, "private") || strings.Contains(trace, srv.Listener.Addr().String()) {
					t.Fatalf("unsafe or missing trace identity: %s", trace)
				}
			})
		}
	}
}
