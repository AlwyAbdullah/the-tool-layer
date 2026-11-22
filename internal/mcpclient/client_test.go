package mcpclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/AlwyAbdullah/the-tool-layer/internal/mcp"
)

func TestDoSendsTransportHeadersAndMeta(t *testing.T) {
	var got struct {
		hdr  http.Header
		body map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.hdr = r.Header.Clone()
		json.NewDecoder(r.Body).Decode(&got.body)
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"isError":false,"content":[{"type":"text","text":"ok"}],"tools":[{"name":"a"},{"name":"b"}]}}`))
	}))
	defer srv.Close()

	c := &Client{URL: srv.URL, Token: "t0k"}
	r, err := c.CallTool(context.Background(), "lookup_order", map[string]any{"order_id": "K-1001"}, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"Mcp-Method": "tools/call", "Mcp-Name": "lookup_order",
		"Mcp-Protocol-Version": mcp.Version, "Authorization": "Bearer t0k"} {
		if got.hdr.Get(k) != want {
			t.Errorf("%s = %q, want %q", k, got.hdr.Get(k), want)
		}
	}
	meta := got.body["params"].(map[string]any)["_meta"].(map[string]any)
	if meta[mcp.IdempotencyMetaKey] != "run-1" || meta["io.modelcontextprotocol/protocolVersion"] != mcp.Version {
		t.Fatalf("_meta = %v", meta)
	}
	if text, isErr := Text(r); text != "ok" || isErr {
		t.Fatalf("text=%q isErr=%v", text, isErr)
	}
	if names := ToolNames(r); !reflect.DeepEqual(names, []string{"a", "b"}) {
		t.Fatalf("names = %v", names)
	}
}

func TestDoParsesRPCErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"method not found"}}`))
	}))
	defer srv.Close()
	r, err := (&Client{URL: srv.URL}).Do(context.Background(), "resources/list", nil, nil)
	if err != nil || r.Status != 404 || r.Error == nil || r.Error.Code != -32601 {
		t.Fatalf("r=%+v err=%v", r, err)
	}
}
