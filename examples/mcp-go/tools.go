package main

import (
	"context"
	"encoding/json"
	"fmt"

	kmcp "github.com/AlwyAbdullah/the-tool-layer/internal/mcp"
	"github.com/AlwyAbdullah/the-tool-layer/internal/toolspec"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const findingsKey = "co.kiosk/findings"

// toolFor turns one of the book's tool specs into an mcp-go tool. The
// spec stays the source of truth: name, schema, annotations, version,
// deprecation. mcp-go only carries it on the wire.
func toolFor(s toolspec.Spec) mcp.Tool {
	desc := s.Description
	if d := s.Deprecated; d != nil {
		desc = fmt.Sprintf("DEPRECATED: use %s. %s", d.Replacement, desc)
	}
	t := mcp.NewToolWithRawSchema(s.Name, desc, s.InputSchema)
	t.Title = s.Title
	a := s.Annotations
	t.Annotations = mcp.ToolAnnotation{Title: s.Title, ReadOnlyHint: a.ReadOnlyHint,
		DestructiveHint: a.DestructiveHint, IdempotentHint: a.IdempotentHint, OpenWorldHint: a.OpenWorldHint}
	t.Meta = mcp.NewMetaFromMap(map[string]any{"co.kiosk/version": s.Version})
	return t
}

// adapt runs one of the book's handlers (internal/kiosk) unchanged
// under mcp-go. The handler still decodes strictly, takes the tenant
// from the context and computes refund amounts itself.
func adapt(h kmcp.Handler) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		r, err := h(ctx, kmcp.Call{Tool: req.Params.Name, Args: rawArgs(req)})
		if err != nil {
			return nil, err // a fault; guard turns it into a safe tool error
		}
		res := &mcp.CallToolResult{Content: []mcp.Content{mcp.NewTextContent(r.Text)}, IsError: r.IsError}
		if r.Structured != nil {
			b, err := json.Marshal(r.Structured)
			if err != nil {
				return nil, err
			}
			res.StructuredContent = r.Structured
			res.Content = append(res.Content, mcp.NewTextContent(string(b)))
		}
		if len(r.Findings) > 0 { // for the audit record; guard removes it
			res.Meta = mcp.NewMetaFromMap(map[string]any{findingsKey: r.Findings})
		}
		return res, nil
	}
}

// takeFindings removes injection findings from the result's _meta and
// returns them for the audit record.
func takeFindings(res *mcp.CallToolResult) []string {
	if res == nil || res.Meta == nil {
		return nil
	}
	var out []string
	switch f := res.Meta.AdditionalFields[findingsKey].(type) {
	case []string:
		out = f
	case []any: // after a replay from the idempotency store
		for _, v := range f {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
	}
	delete(res.Meta.AdditionalFields, findingsKey)
	return out
}
