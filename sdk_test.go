package mosaic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := New(WithEndpoint(server.URL), WithAPIToken("msk_test"), WithRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	client.transport.sleep = func(context.Context, time.Duration) error { return nil }
	client.transport.keyGenerator = func() (string, error) { return "test-key", nil }
	return client
}

func TestResolutionPrecedence(t *testing.T) {
	t.Setenv("MOSAIC_API_URL", "https://mosaic")
	t.Setenv("MAR_ENDPOINT", "https://mar")
	t.Setenv("MOSAIC_API_TOKEN", "mosaic-token")
	t.Setenv("MAR_API_TOKEN", "mar-token")
	t.Setenv("E2B_API_KEY", "msk-e2b")
	client, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if client.Endpoint() != "https://mosaic" {
		t.Fatalf("endpoint = %q", client.Endpoint())
	}
	if client.transport.token != "mosaic-token" || client.transport.credentialSource != "environment variable MOSAIC_API_TOKEN" {
		t.Fatalf("wrong token resolution: %#v", client.transport)
	}
	client, err = New(WithEndpoint("https://explicit/"), WithAPIToken("explicit"), WithRetries(-1))
	if err != nil {
		t.Fatal(err)
	}
	if client.Endpoint() != "https://explicit" || client.transport.token != "explicit" || client.transport.retries != 0 {
		t.Fatalf("wrong explicit resolution: %#v", client.transport)
	}
	t.Setenv("MOSAIC_API_TOKEN", "")
	t.Setenv("MAR_API_TOKEN", "")
	t.Setenv("E2B_API_KEY", "not-mosaic")
	client, _ = New()
	if client.transport.token != "" {
		t.Fatal("unprefixed E2B key was accepted")
	}
}

func TestHeadersAndGeneratedKeyAcrossRetries(t *testing.T) {
	var calls int32
	var keys []string
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		if r.Header.Get("Authorization") != "Bearer msk_test" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("missing headers: %#v", r.Header)
		}
		if atomic.AddInt32(&calls, 1) == 1 {
			http.Error(w, `{"error":"capacity_unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{}`)
	}))
	client.transport.retries = 1
	client.transport.sleep = func(context.Context, time.Duration) error { return nil }
	var out map[string]any
	if err := client.transport.requestInto(context.Background(), http.MethodPost, "/v1/run", map[string]string{"cmd": "true"}, "", &out); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] {
		t.Fatalf("keys = %#v", keys)
	}
}

func TestRetryPolicyAndCancellation(t *testing.T) {
	var calls int
	status := http.StatusTooManyRequests
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, `{"error":"rate_limited"}`, status)
	}))
	client.transport.retries = 1
	ctx, cancel := context.WithCancel(context.Background())
	client.transport.sleep = func(context.Context, time.Duration) error {
		cancel()
		return context.Canceled
	}
	if err := client.transport.request(ctx, http.MethodPost, "/v1/sandboxes/x/exec", nil, ""); !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("non-replayable retry = %v, calls=%d", err, calls)
	}

	calls = 0
	status = http.StatusInternalServerError
	client.transport.sleep = func(context.Context, time.Duration) error { return nil }
	client.transport.retries = 3
	if err := client.transport.request(context.Background(), http.MethodPost, "/v1/sandboxes/x/exec", nil, ""); calls != 1 || err == nil {
		t.Fatalf("non-replayable 500 retry = %v, calls=%d", err, calls)
	}
}

func TestCreateBodiesAndListQuery(t *testing.T) {
	var bodies []map[string]any
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/sandboxes" && r.Method == http.MethodGet {
			if r.URL.Query().Get("metadata.owner") != "test" || r.URL.Query().Get("state") != "running" || r.URL.Query().Get("limit") != "2" || r.URL.Query().Get("cursor") != "abc" {
				t.Fatalf("query = %v", r.URL.Query())
			}
			io.WriteString(w, `{"sandboxes":[],"next_cursor":"next"}`)
			return
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		bodies = append(bodies, body)
		io.WriteString(w, `{"id":"sbx"}`)
	}))
	_, _ = client.CreateSandbox(context.Background(), CreateOptions{})
	_, _ = client.SandboxFromSnapshot(context.Background(), "snap", CreateOptions{MemoryMB: 1024, EnableSSH: true, NetworkAllow: []string{"example.com"}})
	_, _ = client.ListSandboxesPage(context.Background(), ListOptions{Metadata: map[string]string{"owner": "test"}, State: "running", Limit: 2, Cursor: "abc"})
	if bodies[0]["template"] != "base" || bodies[0]["memory_mb"] != float64(4096) || bodies[0]["vcpu"] != float64(2) || bodies[0]["enable_ssh"] != false {
		t.Fatalf("template body = %#v", bodies[0])
	}
	if bodies[1]["snapshot_id"] != "snap" || bodies[1]["template"] != nil || bodies[1]["memory_mb"] != float64(1024) {
		t.Fatalf("snapshot body = %#v", bodies[1])
	}
	network, ok := bodies[1]["network"].(map[string]any)
	if !ok || network["allow"] == nil {
		t.Fatalf("network body = %#v", bodies[1])
	}
}

func TestFileBase64AndSSE(t *testing.T) {
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/sandboxes/sbx/files/content":
			if r.Method == http.MethodGet {
				io.WriteString(w, `{"content_base64":"`+base64.StdEncoding.EncodeToString([]byte("hello"))+`"}`)
				return
			}
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["content_base64"] != base64.StdEncoding.EncodeToString([]byte("hello")) || body["create_parents"] != true {
				t.Fatalf("write body = %#v", body)
			}
			io.WriteString(w, `{"size":5}`)
		case "/v1/sandboxes/sbx/exec":
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "event: stdout\ndata: {\"text\":\"hi\"}\n\nevent: exit\ndata: {\"exit_code\":0}")
		}
	}))
	sandbox := &Sandbox{ID: "sbx", client: client}
	value, err := sandbox.Files().ReadString(context.Background(), "/workspace/a")
	if err != nil || value != "hello" {
		t.Fatalf("read = %q, %v", value, err)
	}
	if _, err := sandbox.Files().WriteString(context.Background(), "/workspace/a", "hello"); err != nil {
		t.Fatal(err)
	}
	stream, err := sandbox.ExecStream(context.Background(), Shell("echo hi"), ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if !stream.Next() || stream.Event().Data != "hi" || !stream.Next() || stream.Event().ExitCode == nil || *stream.Event().ExitCode != 0 || stream.Next() {
		t.Fatalf("SSE stream failed: %#v err=%v", stream.Event(), stream.Err())
	}
}

func TestProcessWaitAndWithSandbox(t *testing.T) {
	var destroyed int32
	var logCalls int32
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/sandboxes" && r.Method == http.MethodPost:
			io.WriteString(w, `{"id":"sbx"}`)
		case r.URL.Path == "/v1/sandboxes/sbx" && r.Method == http.MethodDelete:
			atomic.AddInt32(&destroyed, 1)
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/logs"):
			if atomic.AddInt32(&logCalls, 1) == 1 {
				io.WriteString(w, `{"process_id":"p","stdout":"a","stderr":"","next_stdout_offset":1,"next_stderr_offset":0,"state":"running"}`)
			} else {
				io.WriteString(w, `{"process_id":"p","stdout":"b","stderr":"e","next_stdout_offset":2,"next_stderr_offset":1,"state":"finished"}`)
			}
		case strings.HasSuffix(r.URL.Path, "/processes/p"):
			io.WriteString(w, `{"id":"p","sandbox_id":"sbx","state":"finished","pid":1,"exit_code":2,"started_at_ns":1000000000,"ended_at_ns":3000000000,"pty":false}`)
		}
	}))
	sandbox := &Sandbox{ID: "sbx", client: client}
	process := &Process{ID: "p", sandbox: sandbox}
	result, err := process.Wait(context.Background())
	if err != nil || result.Stdout != "ab" || result.Stderr != "e" || result.Reason != "exit_nonzero" || result.DurationMS != 2000 {
		t.Fatalf("wait = %#v, %v", result, err)
	}
	if err := client.WithSandbox(context.Background(), CreateOptions{}, func(context.Context, *Sandbox) error { return errors.New("callback") }); err == nil || atomic.LoadInt32(&destroyed) != 1 {
		t.Fatalf("with sandbox = %v, destroyed=%d", err, destroyed)
	}
	callbackCtx, cancel := context.WithCancel(context.Background())
	callbackErr := errors.New("callback canceled")
	if err := client.WithSandbox(callbackCtx, CreateOptions{}, func(context.Context, *Sandbox) error {
		cancel()
		return callbackErr
	}); !errors.Is(err, callbackErr) || atomic.LoadInt32(&destroyed) != 2 {
		t.Fatalf("cancelled with sandbox = %v, destroyed=%d", err, destroyed)
	}
}

func TestErrorMappingsAndSnapshot404(t *testing.T) {
	for _, test := range []struct {
		status int
		code   string
		target error
	}{
		{401, "invalid_api_key", ErrAuthentication}, {403, "forbidden", ErrPermission},
		{404, "", ErrNotFound}, {408, "timeout", ErrTimeout}, {429, "rate_limited", ErrRateLimited},
		{400, "unsupported_template", ErrUnsupportedTemplate}, {400, "unsupported_shape", ErrUnsupportedShape},
		{400, "unknown_field", ErrUnknownField},
	} {
		err := apiError(test.status, `{"error":"`+test.code+`","message":"bad"}`, "environment variable MOSAIC_API_TOKEN")
		if !errors.Is(err, test.target) {
			t.Fatalf("%d/%s did not map: %v", test.status, test.code, err)
		}
	}
	err := apiError(401, `{"error":"invalid_api_key","message":"bad"}`, "environment variable MOSAIC_API_TOKEN")
	if !strings.Contains(err.Error(), "Credential source: environment variable MOSAIC_API_TOKEN.") {
		t.Fatalf("credential suffix missing: %v", err)
	}
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"missing"}`, http.StatusNotFound)
	}))
	value, snapshotErr := client.GetSnapshot(context.Background(), "missing")
	if snapshotErr != nil || value != nil {
		t.Fatalf("snapshot 404 = %#v, %v", value, snapshotErr)
	}
}

func TestRetryAfterAndRetryEnvironment(t *testing.T) {
	var calls int
	var delays []time.Duration
	client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "1")
			http.Error(w, `{"error":"rate_limited"}`, http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, `{}`)
	}))
	client.transport.retries = 1
	client.transport.sleep = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}
	if err := client.transport.request(context.Background(), http.MethodPost, "/v1/run", nil, ""); err != nil {
		t.Fatal(err)
	}
	if len(delays) != 1 || delays[0] != time.Second {
		t.Fatalf("retry-after delay = %#v", delays)
	}

	calls = 0
	delays = nil
	client.transport.sleep = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}
	cappedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "20")
			http.Error(w, `{"error":"rate_limited"}`, http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, `{}`)
	}))
	t.Cleanup(cappedServer.Close)
	client.transport.endpoint = cappedServer.URL
	if err := client.transport.request(context.Background(), http.MethodPost, "/v1/run", nil, ""); err != nil {
		t.Fatal(err)
	}
	if len(delays) != 1 || delays[0] != 8*time.Second {
		t.Fatalf("capped retry-after delay = %#v", delays)
	}

	t.Setenv("MOSAIC_RETRIES", "0")
	var zeroCalls int
	zeroServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zeroCalls++
		http.Error(w, `{"error":"capacity_unavailable"}`, http.StatusServiceUnavailable)
	}))
	t.Cleanup(zeroServer.Close)
	zero, err := New(WithEndpoint(zeroServer.URL))
	if err != nil {
		t.Fatal(err)
	}
	if zero.transport.retries != 0 {
		t.Fatalf("MOSAIC_RETRIES=0 produced %d retries", zero.transport.retries)
	}
	if err := zero.transport.request(context.Background(), http.MethodPost, "/v1/run", nil, ""); err == nil || zeroCalls != 1 {
		t.Fatalf("MOSAIC_RETRIES=0 request = %v, calls=%d", err, zeroCalls)
	}
	t.Setenv("MOSAIC_RETRIES", "not-a-number")
	fallback, err := New(WithEndpoint("https://example.test"))
	if err != nil {
		t.Fatal(err)
	}
	if fallback.transport.retries != 3 {
		t.Fatalf("invalid MOSAIC_RETRIES produced %d retries", fallback.transport.retries)
	}
}

func TestEmptyResponses(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
	}{
		{name: "no content", status: http.StatusNoContent},
		{name: "empty body", status: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(test.status)
			}))
			var result map[string]any
			if err := client.transport.requestInto(context.Background(), http.MethodGet, "/v1/limits", nil, "", &result); err != nil {
				t.Fatal(err)
			}
			if result != nil {
				t.Fatalf("result = %#v, want nil zero value", result)
			}
		})
	}
}

func TestUploadBoundaries(t *testing.T) {
	t.Run("exact single put limit", func(t *testing.T) {
		var calls int
		client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			content, err := base64.StdEncoding.DecodeString(body["content_base64"].(string))
			if err != nil || len(content) != MaxUploadBytes {
				t.Fatalf("decoded upload size = %d, %v", len(content), err)
			}
			if _, ok := body["append"]; ok {
				t.Fatal("single upload unexpectedly has append")
			}
			io.WriteString(w, `{"size":8388608}`)
		}))
		size, err := (&Sandbox{ID: "sbx", client: client}).Files().Write(context.Background(), "/workspace/a", make([]byte, MaxUploadBytes))
		if err != nil || size != MaxUploadBytes || calls != 1 {
			t.Fatalf("single upload = size %d, err %v, calls %d", size, err, calls)
		}
	})

	t.Run("chunked upload", func(t *testing.T) {
		var calls int
		var sizes []int
		var appends []bool
		client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			content, err := base64.StdEncoding.DecodeString(body["content_base64"].(string))
			if err != nil {
				t.Fatal(err)
			}
			sizes = append(sizes, len(content))
			_, appended := body["append"]
			appends = append(appends, appended && body["append"] == true)
			w.WriteHeader(http.StatusNoContent)
		}))
		content := make([]byte, MaxUploadBytes+1)
		size, err := (&Sandbox{ID: "sbx", client: client}).Files().Write(context.Background(), "/workspace/a", content)
		if err != nil || size != int64(len(content)) || calls != 2 {
			t.Fatalf("chunked upload = size %d, err %v, calls %d", size, err, calls)
		}
		if len(sizes) != 2 || sizes[0] != UploadChunkBytes || sizes[1] != len(content)-UploadChunkBytes {
			t.Fatalf("chunk sizes = %#v", sizes)
		}
		if len(appends) != 2 || appends[0] || !appends[1] {
			t.Fatalf("append flags = %#v", appends)
		}
	})

	t.Run("maximum rejected before request", func(t *testing.T) {
		var calls int
		client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
		}))
		_, err := (&Sandbox{ID: "sbx", client: client}).Files().Write(context.Background(), "/workspace/a", make([]byte, MaxUploadFileBytes+1))
		if err == nil || calls != 0 {
			t.Fatalf("oversized upload = %v, calls %d", err, calls)
		}
	})

	t.Run("partial failure preserves landed bytes and cause", func(t *testing.T) {
		var calls int
		client := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.Error(w, `{"error":"write_failed","message":"disk full"}`, http.StatusInternalServerError)
		}))
		_, err := (&Sandbox{ID: "sbx", client: client}).Files().Write(context.Background(), "/workspace/a", make([]byte, UploadChunkBytes*2))
		var partial *Error
		if !errors.As(err, &partial) || partial.Code != "file_upload_partial" || !strings.Contains(partial.Message, "6291456 bytes landed") {
			t.Fatalf("partial upload error = %#v, %v", partial, err)
		}
		if strings.Contains(err.Error(), "(0 ") {
			t.Fatalf("partial upload rendered HTTP-like zero status: %v", err)
		}
		var original *Error
		originalErr := errors.Unwrap(partial)
		if !errors.As(originalErr, &original) || original.Status != http.StatusInternalServerError || !errors.Is(err, originalErr) {
			t.Fatalf("original transport error not reachable: %v", err)
		}
	})
}

func TestExecStreamCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server does not support flushing")
		}
		_, _ = io.WriteString(w, "event: stdout\ndata: {\"text\":\"partial\"}\n\n")
		flusher.Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	client, err := New(WithEndpoint(server.URL), WithRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	stream, err := (&Sandbox{ID: "sbx", client: client}).ExecStream(ctx, Shell("echo hi"), ExecOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Next() || stream.Event().Data != "partial" {
		t.Fatalf("first stream event = %#v, err=%v", stream.Event(), stream.Err())
	}
	cancel()
	next := stream.Next()
	if next || !errors.Is(stream.Err(), context.Canceled) {
		t.Fatalf("cancelled stream = next %v, err %v", next, stream.Err())
	}
}
