package mosaic

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// SecretReference resolves a namespaced bundle before compute is allocated.
type SecretReference struct {
	Name         string   `json:"name"`
	Namespace    string   `json:"namespace"`
	RequiredKeys []string `json:"required_keys,omitempty"`
}

// SecretBundleInfo contains metadata only. Injected values are readable by guests.
type SecretBundleInfo struct {
	Name          string   `json:"name"`
	Namespace     string   `json:"namespace"`
	Version       int      `json:"version"`
	Keys          []string `json:"keys"`
	Consumers     []string `json:"consumers"`
	ValueBytes    int      `json:"value_bytes"`
	Readable      bool     `json:"readable"`
	GuestReadable bool     `json:"guest_readable"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
}

type SecretBundleWriteOptions struct {
	// Mode is create (default), merge or replace. Updates require Version.
	Mode    string
	Version int
	// Explicit API-key IDs. Nil preserves grants on update, empty removes them.
	Consumers []string
}

func secretBundlePath(namespace, name string) string {
	path := "/v1/secret-bundles/" + url.PathEscape(namespace)
	if name != "" {
		path += "/" + url.PathEscape(name)
	}
	return path
}

// WriteSecretBundle requires organization administration. Creation authority
// does not grant bundle consumption. Values are never returned in metadata.
// Merge an empty values map with explicit Consumers to change access only.
func (c *Client) WriteSecretBundle(ctx context.Context, namespace, name string, values map[string]string, options SecretBundleWriteOptions) (*SecretBundleInfo, error) {
	mode := options.Mode
	if mode == "" {
		mode = "create"
	}
	method := map[string]string{"create": http.MethodPost, "merge": http.MethodPatch, "replace": http.MethodPut}[mode]
	if method == "" {
		return nil, errors.New("expected create, merge or replace mode")
	}
	path := secretBundlePath(namespace, name)
	if mode != "create" {
		if options.Version < 1 {
			return nil, errors.New("a positive expected version is required")
		}
		path += fmt.Sprintf("?version=%d", options.Version)
	}
	body := map[string]any{}
	if mode != "merge" || options.Consumers == nil || len(values) > 0 {
		body["values"] = values
	}
	if options.Consumers != nil {
		body["consumers"] = options.Consumers
	}
	var result SecretBundleInfo
	if err := c.transport.requestInto(ctx, method, path, body, "", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *Client) ListSecretBundles(ctx context.Context, namespace string) ([]SecretBundleInfo, error) {
	bundles := []SecretBundleInfo{}
	cursor := ""
	for {
		path := secretBundlePath(namespace, "") + "?limit=1000"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		var page struct {
			Bundles    []SecretBundleInfo `json:"bundles"`
			NextCursor string             `json:"next_cursor"`
		}
		if err := c.transport.requestInto(ctx, http.MethodGet, path, nil, "", &page); err != nil {
			return nil, err
		}
		bundles = append(bundles, page.Bundles...)
		if page.NextCursor == "" {
			return bundles, nil
		}
		cursor = page.NextCursor
	}
}

// DeleteSecretBundle stops future resolution. Existing guests retain plaintext.
func (c *Client) DeleteSecretBundle(ctx context.Context, namespace, name string, version int) error {
	if version < 1 {
		return errors.New("a positive expected version is required")
	}
	return c.transport.request(ctx, http.MethodDelete, secretBundlePath(namespace, name)+fmt.Sprintf("?version=%d", version), nil, "")
}
