package mosaic

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	ErrAuthentication      = errors.New("authentication failed")
	ErrPermission          = errors.New("permission denied")
	ErrNotFound            = errors.New("not found")
	ErrTimeout             = errors.New("request timed out")
	ErrRateLimited         = errors.New("rate limited")
	ErrUnsupportedTemplate = errors.New("unsupported template")
	ErrUnsupportedShape    = errors.New("unsupported shape")
	ErrUnknownField        = errors.New("unknown field")
)

type Error struct {
	Status             int
	Code               string
	Message            string
	RequestID          string
	Remediation        string
	CredentialSource   string
	SupportedTemplates []string
	SupportedShape     map[string]any
	Field              string
	RetryAfterSeconds  float64
	Body               string
	cause              error
	category           error
}

func (e *Error) Error() string {
	message := strings.TrimSpace(e.Message)
	if message == "" {
		message = boundedBodyHint(e.Body)
	}
	if message == "" {
		message = fmt.Sprintf("HTTP %d", e.Status)
	}
	code := ""
	if e.Code != "" {
		code = " " + e.Code
	}
	text := message
	if e.Status != 0 {
		text = fmt.Sprintf("Mosaic request failed (%d%s): %s", e.Status, code, message)
	}
	if e.Remediation != "" && !strings.Contains(text, e.Remediation) {
		text += " " + e.Remediation
	}
	if e.cause != nil {
		text += ": " + e.cause.Error()
	}
	return text
}

func (e *Error) Unwrap() error { return e.cause }

func (e *Error) Is(target error) bool {
	return target == e.category
}

func boundedBodyHint(body string) string {
	text := strings.TrimSpace(body)
	if text == "" || strings.HasPrefix(text, "<") || strings.Contains(text, "<html") ||
		strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
		return ""
	}
	if len(text) > 160 {
		return text[:157] + "..."
	}
	return text
}

func apiError(status int, body, credentialSource string) *Error {
	var payload map[string]any
	_ = json.Unmarshal([]byte(body), &payload)
	code, _ := payload["error"].(string)
	message, _ := payload["message"].(string)
	requestID, _ := payload["request_id"].(string)
	remediation, _ := payload["remediation"].(string)
	if remediation == "" && (status == http.StatusUnauthorized || code == "invalid_api_key" || code == "missing_or_invalid_api_key") {
		remediation = "Set MOSAIC_API_TOKEN to a valid Mosaic API key or sign in at https://sandbox.mosaicos.com/start/."
	}
	err := &Error{
		Status:           status,
		Code:             code,
		Message:          strings.TrimSpace(message),
		RequestID:        requestID,
		Remediation:      remediation,
		CredentialSource: credentialSource,
		Body:             body,
	}
	if values, ok := payload["supported_templates"].([]any); ok {
		for _, value := range values {
			if object, ok := value.(map[string]any); ok {
				if id, ok := object["id"].(string); ok {
					err.SupportedTemplates = append(err.SupportedTemplates, id)
					continue
				}
			}
			err.SupportedTemplates = append(err.SupportedTemplates, fmt.Sprint(value))
		}
	}
	if shape, ok := payload["supported_shape"].(map[string]any); ok {
		err.SupportedShape = shape
	}
	err.Field, _ = payload["field"].(string)
	if value, ok := payload["retry_after_seconds"].(float64); ok {
		err.RetryAfterSeconds = value
	}
	switch {
	case status == 401 || code == "invalid_api_key" || code == "missing_or_invalid_api_key":
		err.category = ErrAuthentication
	case status == 403 || code == "forbidden" || code == "insufficient_scope":
		err.category = ErrPermission
	case status == 404:
		err.category = ErrNotFound
	case status == 408 || code == "timeout":
		err.category = ErrTimeout
	case status == 429 || code == "rate_limited":
		err.category = ErrRateLimited
	case code == "unsupported_template":
		err.category = ErrUnsupportedTemplate
	case code == "unsupported_shape":
		err.category = ErrUnsupportedShape
	case code == "unknown_field":
		err.category = ErrUnknownField
	}
	if (status == 401 || code == "invalid_api_key" || code == "missing_or_invalid_api_key") && credentialSource != "" {
		if err.Message == "" {
			err.Message = boundedBodyHint(body)
		}
		err.Message += " Credential source: " + credentialSource + "."
	}
	return err
}

type transport struct {
	endpoint         string
	token            string
	credentialSource string
	client           *http.Client
	retries          int
	sleep            func(context.Context, time.Duration) error
	keyGenerator     func() (string, error)
}

func WithEndpoint(endpoint string) Option {
	return func(o *options) { o.endpoint = endpoint }
}

func WithAPIToken(token string) Option {
	return func(o *options) {
		o.token = token
		o.tokenSource = "explicit WithAPIToken option"
	}
}

func WithHTTPClient(client *http.Client) Option {
	return func(o *options) { o.httpClient = client }
}

func WithRetries(attempts int) Option {
	return func(o *options) { o.retries = &attempts }
}

func New(opts ...Option) (*Client, error) {
	var configured options
	for _, option := range opts {
		option(&configured)
	}
	variables := os.Environ()
	env := make(map[string]string, len(variables))
	for _, value := range variables {
		if key, val, ok := strings.Cut(value, "="); ok {
			env[key] = val
		}
	}
	endpoint := configured.endpoint
	if endpoint == "" {
		endpoint = env["MOSAIC_API_URL"]
	}
	if endpoint == "" {
		endpoint = env["MAR_ENDPOINT"]
	}
	if endpoint == "" {
		endpoint = "https://sandbox.mosaicos.com"
	}
	endpoint = strings.TrimSuffix(endpoint, "/")
	token := configured.token
	source := configured.tokenSource
	if token == "" {
		if env["MOSAIC_API_TOKEN"] != "" {
			token, source = env["MOSAIC_API_TOKEN"], "environment variable MOSAIC_API_TOKEN"
		} else if env["MAR_API_TOKEN"] != "" {
			token, source = env["MAR_API_TOKEN"], "environment variable MAR_API_TOKEN"
		} else if strings.HasPrefix(env["E2B_API_KEY"], "msk_") {
			token, source = env["E2B_API_KEY"], "environment variable E2B_API_KEY"
		}
	}
	retries := 3
	if configured.retries != nil {
		retries = *configured.retries
	} else if value, ok := env["MOSAIC_RETRIES"]; ok {
		if parsed, err := strconv.Atoi(value); err == nil {
			retries = parsed
		}
	}
	if retries < 0 {
		retries = 0
	}
	client := configured.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	t := &transport{
		endpoint: endpoint, token: token, credentialSource: source, client: client,
		retries: retries, sleep: sleepContext, keyGenerator: randomUUID,
	}
	return &Client{transport: t}, nil
}

func (c *Client) Endpoint() string { return c.transport.endpoint }

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func randomUUID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(value[0:4]), hex.EncodeToString(value[4:6]),
		hex.EncodeToString(value[6:8]), hex.EncodeToString(value[8:10]),
		hex.EncodeToString(value[10:16])), nil
}

func replayable(method, path string) bool {
	if method == http.MethodGet || method == http.MethodHead {
		return true
	}
	if method == http.MethodPut && strings.SplitN(path, "?", 2)[0] == "/v1/sandboxes" {
		return true
	}
	if method != http.MethodPost {
		return false
	}
	return replayablePattern.MatchString(path)
}

var replayablePattern = regexp.MustCompile(`^/v1/(sandboxes|sandboxes/async|run|volumes|environments|snapshots/[^/]+/replicas|sandboxes/[^/]+/(fork|snapshots|previews|fanout))(\?|$)`)

var retryableStatuses = map[int]bool{429: true, 500: true, 502: true, 503: true, 504: true}

func retryable(status int, body string) bool {
	if retryableStatuses[status] {
		return true
	}
	var payload map[string]any
	if json.Unmarshal([]byte(body), &payload) == nil {
		code, _ := payload["error"].(string)
		return code == "idempotency_key_in_flight" || code == "reattach_in_flight"
	}
	return false
}

func retryDelay(attempt int, retryAfter string) time.Duration {
	backoff := 250 * time.Millisecond * time.Duration(math.Pow(2, float64(attempt)))
	if backoff > 8*time.Second {
		backoff = 8 * time.Second
	}
	if retryAfter == "" {
		return backoff
	}
	if seconds, err := strconv.ParseFloat(strings.TrimSpace(retryAfter), 64); err == nil {
		requested := time.Duration(seconds * float64(time.Second))
		if requested > 8*time.Second {
			requested = 8 * time.Second
		}
		if requested > backoff {
			return requested
		}
	}
	return backoff
}

func (t *transport) request(ctx context.Context, method, path string, body any, callerKey string) error {
	var output any
	return t.requestInto(ctx, method, path, body, callerKey, &output)
}

func (t *transport) requestInto(ctx context.Context, method, path string, body any, callerKey string, output any) error {
	replay := replayable(method, path)
	key := callerKey
	if key == "" && replay && method == http.MethodPost {
		var err error
		key, err = t.keyGenerator()
		if err != nil {
			return err
		}
	}
	jsonBody, err := json.Marshal(body)
	if body == nil {
		jsonBody = nil
	}
	if err != nil {
		return err
	}
	for attempt := 0; ; attempt++ {
		request, err := http.NewRequestWithContext(ctx, method, t.endpoint+path, bytes.NewReader(jsonBody))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/json")
		if t.token != "" {
			request.Header.Set("Authorization", "Bearer "+t.token)
		}
		if key != "" {
			request.Header.Set("Idempotency-Key", key)
		}
		response, err := t.client.Do(request)
		if err != nil {
			if attempt >= t.retries || !replay {
				return err
			}
			if err := t.sleep(ctx, retryDelay(attempt, "")); err != nil {
				return err
			}
			continue
		}
		detail, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil {
			if attempt >= t.retries || !replay {
				return readErr
			}
			if err := t.sleep(ctx, retryDelay(attempt, "")); err != nil {
				return err
			}
			continue
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			bodyText := string(detail)
			canRetry := attempt < t.retries && (response.StatusCode == 429 || (replay && retryable(response.StatusCode, bodyText)))
			if canRetry {
				if err := t.sleep(ctx, retryDelay(attempt, response.Header.Get("Retry-After"))); err != nil {
					return err
				}
				continue
			}
			apiErr := apiError(response.StatusCode, bodyText, t.credentialSource)
			if apiErr.RetryAfterSeconds == 0 {
				if seconds, parseErr := strconv.ParseFloat(response.Header.Get("Retry-After"), 64); parseErr == nil {
					apiErr.RetryAfterSeconds = seconds
				}
			}
			return apiErr
		}
		if output == nil || len(detail) == 0 || response.StatusCode == http.StatusNoContent {
			return nil
		}
		if err := json.Unmarshal(detail, output); err != nil {
			return err
		}
		return nil
	}
}

func (t *transport) open(ctx context.Context, method, path string, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if body == nil {
		data = nil
	}
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, method, t.endpoint+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	if t.token != "" {
		request.Header.Set("Authorization", "Bearer "+t.token)
	}
	response, err := t.client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		return nil, apiError(response.StatusCode, readBody(response.Body), t.credentialSource)
	}
	return response, nil
}

func readBody(body io.Reader) string {
	data, _ := io.ReadAll(body)
	return string(data)
}
