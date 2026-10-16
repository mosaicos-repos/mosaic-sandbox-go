package mosaic

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

type InvocationSecrets struct{ values map[string]string }

func (s InvocationSecrets) Format(state fmt.State, verb rune) {
	fmt.Fprint(state, "InvocationSecrets{Values:[redacted]}")
}
func (s InvocationSecrets) MarshalJSON() ([]byte, error) {
	return []byte("{\"values\":\"[redacted]\"}"), nil
}
func (s InvocationSecrets) Metadata() map[string]any {
	return map[string]any{"key_count": len(s.values), "guest_readable": true, "invocation_scoped": true}
}
func (s InvocationSecrets) wireValues() map[string]string { return cloneStringMap(s.values) }

var invocationKey = regexp.MustCompile("^[A-Za-z_][A-Za-z0-9_]{0,63}$")

func NewInvocationSecrets(values map[string]string) (*InvocationSecrets, error) {
	if len(values) == 0 || len(values) > 256 {
		return nil, errors.New("invalid private invocation values")
	}
	total := 0
	for key, value := range values {
		if !invocationKey.MatchString(key) || strings.HasPrefix(key, "MOSAIC_") || value == "" || strings.ContainsRune(value, 0) || !utf8.ValidString(value) || len(value) > 32768 {
			return nil, errors.New("invalid private invocation values")
		}
		total += len(key) + len(value)
	}
	if total > 131072 {
		return nil, errors.New("private invocation values exceed delivery limit")
	}
	return &InvocationSecrets{values: cloneStringMap(values)}, nil
}
func InvocationSecretsFromJSON(source io.Reader) (*InvocationSecrets, error) {
	data, err := io.ReadAll(io.LimitReader(source, 1024*1024+1))
	if err != nil || len(data) > 1024*1024 {
		return nil, errors.New("unable to read bounded private invocation input")
	}
	var values map[string]string
	if json.Unmarshal(data, &values) != nil {
		return nil, errors.New("invalid private invocation JSON")
	}
	return NewInvocationSecrets(values)
}
func InvocationSecretsFromEnvironment(keys []string) (*InvocationSecrets, error) {
	values := map[string]string{}
	for _, key := range keys {
		value, ok := os.LookupEnv(key)
		if !ok {
			return nil, errors.New("a requested private environment variable is unset")
		}
		values[key] = value
	}
	return NewInvocationSecrets(values)
}
func InvocationSecretsFromDotenv(source io.Reader) (*InvocationSecrets, error) {
	data, err := io.ReadAll(io.LimitReader(source, 1024*1024+1))
	if err != nil || len(data) > 1024*1024 || !utf8.Valid(data) {
		return nil, errors.New("unable to read bounded private invocation input")
	}
	values := map[string]string{}
	invalid := func() (*InvocationSecrets, error) { return nil, errors.New("invalid private invocation dotenv") }
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if !ok {
			return invalid()
		}
		if _, exists := values[key]; exists {
			return invalid()
		}
		if strings.HasPrefix(value, "\"") {
			if json.Unmarshal([]byte(value), &value) != nil {
				return invalid()
			}
		} else if strings.HasPrefix(value, "'") {
			if len(value) < 2 || !strings.HasSuffix(value, "'") || strings.Contains(value[1:len(value)-1], "'") {
				return invalid()
			}
			value = value[1 : len(value)-1]
		} else {
			value = strings.TrimRight(strings.SplitN(value, " #", 2)[0], " \t")
		}
		values[key] = value
	}
	return NewInvocationSecrets(values)
}
func validatePrivateEndpoint(endpoint string) error {
	target, err := url.Parse(endpoint)
	if err != nil || target.User != nil || target.RawQuery != "" || target.Fragment != "" || target.Host == "" {
		return errors.New("invalid private invocation endpoint")
	}
	if target.Scheme == "https" {
		return nil
	}
	ip := net.ParseIP(target.Hostname())
	if target.Scheme != "http" || (target.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
		return errors.New("private invocation input requires HTTPS outside loopback fixtures")
	}
	return nil
}
func (s InvocationSecrets) contains(text string) bool {
	for _, value := range s.values {
		if strings.Contains(text, value) || strings.Contains(text, base64.StdEncoding.EncodeToString([]byte(value))) {
			return true
		}
	}
	return false
}
func (t *transport) privateInvocation(ctx context.Context, body map[string]any, values *InvocationSecrets, result *OneShotResult) error {
	if err := validatePrivateEndpoint(t.endpoint); err != nil {
		return err
	}
	if values == nil || len(values.values) == 0 {
		return errors.New("invalid private invocation values")
	}
	client, err := privateHTTPClient(t.client)
	if err != nil {
		return err
	}
	deadline := 90 * time.Second
	if timeout, ok := body["timeout_ms"].(int); ok && timeout > 30000 {
		deadline = time.Duration(timeout)*time.Millisecond + 60*time.Second
	}
	if t.client.Timeout > 0 && t.client.Timeout < deadline {
		deadline = t.client.Timeout
	}
	client.Timeout = deadline
	body["invocation_secrets"] = values.wireValues()
	data, err := json.Marshal(body)
	if err != nil || len(data) > 1024*1024 {
		return errors.New("private invocation request exceeds limit")
	}
	request, err := http.NewRequestWithContext(ctx, "POST", t.endpoint+"/v1/run/ephemeral", bytes.NewReader(data))
	if err != nil {
		return errors.New("invalid private invocation request")
	}
	request.Header.Set("Content-Type", "application/json")
	if t.token != "" {
		request.Header.Set("Authorization", "Bearer "+t.token)
	}
	response, err := client.Do(request)
	if err != nil {
		return &Error{Code: "invocation_transport_failed", Message: "Private invocation transport failed"}
	}
	defer response.Body.Close()
	data, err = io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	if err != nil || len(data) > 1024*1024 {
		return &Error{Code: "invocation_response_limit", Message: "Private invocation response exceeds limit"}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &Error{Status: response.StatusCode, Code: "invocation_failed", Message: "Private invocation failed"}
	}
	if json.Unmarshal(data, result) != nil {
		return &Error{Code: "invocation_transport_failed", Message: "Invalid private invocation response"}
	}
	if values.contains(result.Stdout) || values.contains(result.Stderr) {
		return &Error{Code: "invocation_output_suppressed", Message: "Private invocation output suppressed"}
	}
	return nil
}
