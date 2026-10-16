package mosaic

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestConnectionGrantRedactionAndRequest(t *testing.T) {
	token := "mcg_live_" + strings.Repeat("a", 64)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("capability not delivered")
		}
		if strings.HasSuffix(r.URL.Path, "/redirect") {
			w.Header().Set("Location", "/unexpected")
			w.WriteHeader(302)
			return
		}
		w.Write([]byte("fixture-body"))
	}))
	defer server.Close()
	info := ConnectionGrantInfo{ID: "grant", ConnectionID: "resource", ProxyURL: server.URL + "/connect/resource", Operations: []string{"read"}}
	grant, err := newConnectionGrant(info, token, server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(grant)
	for _, display := range []string{string(data), fmt.Sprintf("%v", grant), fmt.Sprintf("%#v", grant), fmt.Sprintf("%+v", grant)} {
		if strings.Contains(display, token) {
			t.Fatal("capability in representation")
		}
	}
	response, err := grant.Request(context.Background(), "GET", "/read", nil, nil)
	if err != nil || string(response.Body) != "fixture-body" {
		t.Fatal("fixture call failed", err)
	}
	if strings.Contains(fmt.Sprintf("%#v", *response), "fixture-body") {
		t.Fatal("body in response representation")
	}
	if _, err = grant.Request(context.Background(), "GET", "/redirect", nil, nil); err == nil {
		t.Fatal("redirect accepted")
	}
	if calls != 2 {
		t.Fatal("redirect followed or retried")
	}
	if _, err = grant.Request(context.Background(), "GET", "//other.test/", nil, nil); err == nil {
		t.Fatal("absolute path accepted")
	}
	if _, err = grant.Request(context.Background(), "GET", "/read", nil, http.Header{"Authorization": []string{"override"}}); err == nil {
		t.Fatal("auth override accepted")
	}
	if calls != 2 {
		t.Fatal("invalid request forwarded")
	}
}
func TestConnectionProxyOriginAndCAS(t *testing.T) {
	token := "mcg_live_" + strings.Repeat("a", 64)
	if _, err := newConnectionGrant(ConnectionGrantInfo{ConnectionID: "resource", ProxyURL: "https://other.test/connect/resource"}, token, "https://sandbox.test", http.DefaultClient); err == nil {
		t.Fatal("different origin accepted")
	}
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.String())
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte("{\"name\":\"service\",\"revision\":2}"))
	}))
	defer server.Close()
	client, err := New(WithEndpoint(server.URL), WithAPIToken("synthetic"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := client.ReplaceConnection(context.Background(), "prod", "service", ConnectionPolicy{}, 1)
	if err != nil || info.Revision != 2 {
		t.Fatal(err)
	}
	if requests[0] != "PUT /v1/connections/prod/service?revision=1" {
		t.Fatal(requests)
	}
	if err := client.RevokeConnection(context.Background(), "prod", "service", 0); err == nil {
		t.Fatal("missing revision accepted")
	}
}

func TestConnectionExplicitEmptyWorkloadIDsStayEmpty(t *testing.T) {
	b, err := json.Marshal(ConnectionWorkloads{Mode: "caller_owned", SandboxIDs: []string{}})
	if err != nil || !strings.Contains(string(b), "\"sandbox_ids\":[]") {
		t.Fatal("empty deny-all workload set omitted", string(b), err)
	}
}

func TestConnectionAmbiguousGrantIssueNotRetried(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		w.Write([]byte("{\"error\":\"fixture_failure\"}"))
	}))
	defer server.Close()
	client, err := New(WithEndpoint(server.URL), WithAPIToken("synthetic"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.IssueConnectionGrant(context.Background(), "dev", "service", "sandbox", []string{"read"}, 120)
	if err == nil || calls != 1 {
		t.Fatal("ambiguous issuance retried", calls, err)
	}
}

func TestConnectionClientUsesVerifiedTLSAndHTTP1(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 1 || r.TLS == nil {
			t.Error("expected TLS HTTP/1.1")
		}
		w.Write([]byte("verified-fixture"))
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	grant, err := newConnectionGrant(ConnectionGrantInfo{ConnectionID: "resource", ProxyURL: server.URL + "/connect/resource"}, "mcg_live_"+strings.Repeat("a", 64), server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	response, err := grant.Request(context.Background(), "GET", "/read", nil, nil)
	if err != nil || string(response.Body) != "verified-fixture" {
		t.Fatal("verified TLS call failed", err)
	}
	wrong, err := newConnectionGrant(ConnectionGrantInfo{ConnectionID: "resource", ProxyURL: server.URL + "/connect/resource"}, "mcg_live_"+strings.Repeat("a", 64), server.URL, http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = wrong.Request(context.Background(), "GET", "/read", nil, nil); err == nil {
		t.Fatal("untrusted fixture certificate accepted")
	}
}

func TestNetworkProfileAllowlistResolution(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.EscapedPath() != "/v1/network-profiles/prod%20space/pilot%2Fname/resolve" {
			t.Error("incorrect metadata endpoint")
		}
		var body map[string]int
		if json.NewDecoder(r.Body).Decode(&body) != nil || body["revision"] != 3 {
			t.Error("revision missing")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"revision":3,"readiness":"qualified","usable":true,"egress_ips":["8.8.8.8","2606:4700:4700::1111"]}`))
	}))
	defer server.Close()
	client, err := New(WithEndpoint(server.URL), WithAPIToken("synthetic"))
	if err != nil {
		t.Fatal(err)
	}
	info, err := client.ResolveNetworkProfile(context.Background(), "prod space", "pilot/name", 3)
	if err != nil || info.Revision != 3 || !info.Usable || len(info.EgressIPs) != 2 {
		t.Fatal("metadata resolution failed", err)
	}
	if _, err = client.ResolveNetworkProfile(context.Background(), "prod", "pilot", 0); err == nil {
		t.Fatal("missing revision accepted")
	}
}
