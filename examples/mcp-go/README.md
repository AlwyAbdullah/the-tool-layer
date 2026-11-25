# The Tool Layer checks on mcp-go

The book's Kiosk & Co tool server, running on [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go) (v1.6.0, MCP revision `2026-07-28`) instead of the hand-written server in `internal/mcp`.

mcp-go handles the protocol: `server/discover`, `tools/list`, `tools/call`, headers, version negotiation, and legacy `initialize` clients. Everything the book is about stays in your code: who may call, for which tenant, how often, how many times, and what gets recorded. The checks are the **same packages** as the rest of the repository (`auth`, `tenant`, `ratelimit`, `idem`, `audit`, `toolspec`, `kiosk`), unchanged.

```bash
cd examples/mcp-go
go test ./...      # the repo's end-to-end walkthrough, run against mcp-go
go run .           # localhost server; prints a dev token

# second terminal, from the repository root:
go run ./cmd/kiosk-client -url http://127.0.0.1:8080/mcp -token <token>
```

The client prints the same nine steps as against `cmd/kiosk-mcp`: 401 with resource metadata, a scope-filtered tool list, a cross-tenant lookup that looks like a missing order, a readable enum error, an idempotent replay, a 429 with `Retry-After`, then "nothing left to refund".

## Where each check lives

There are three hooks. Each check goes where it has what it needs.

| Check (chapter) | Hook | Why there |
|---|---|---|
| Token: issuer, audience, expiry, tenant (4) | `gate`: net/http middleware in front of mcp-go | A rejection is an HTTP **401** with a `WWW-Authenticate` challenge. A tool handler can't set status codes. |
| Scope per tool, 403 `insufficient_scope` (4) | `gate` | Also a status code plus a challenge naming the missing scope. |
| Burst limit, cost-weighted, 429 + `Retry-After` (6) | `gate` | Status code and header. The gate reads the tool name from the body, so a refund costs 5 tokens and a lookup costs 1. |
| Principal into the handler's context (5) | `server.WithHTTPContextFunc(carry)` | Handlers take the tenant from the context, never from arguments. |
| `tools/list` shows only allowed, non-retired tools (4, 8) | `server.WithToolFilter(visible)` | mcp-go applies the filter to `tools/call` as well, so a hidden tool can't be called. |
| Daily quota and usage meter (6) | `server.WithToolHandlerMiddleware(guard)` | Spent only after the gate admits the call. |
| Idempotency key in `_meta`, fingerprint, replay (7) | `guard` → `once` | Needs the parsed `_meta` and the handler's result. |
| Deprecation warning, retired tools (8) | `guard` | Appends the warning to the result the model reads. |
| Faults become a safe tool error with a trace ID (10) | `guard` | The model gets "check state before retrying", and the audit record gets the real error. |
| One audit record per call, argument hash only (10) | opened in `gate`, completed in `guard` | Rate-limited and denied calls get recorded too, not only the ones that reach a handler. |
| Strict decoding, closed enums, server-computed refund amount (3, 7) | the `internal/kiosk` handlers, via `adapt` | The tools themselves don't change. |

## Things worth knowing

- **Not everything can be tool middleware.** SDK tool middleware runs after the request has been accepted, so it can't return 401, 403 or 429. Put auth and burst limits in HTTP middleware in front of the SDK, as here, or your rate limit becomes a tool result that the agent may just retry.
- **mcp-go enforces `2026-07-28` request metadata.** A modern request without `_meta` `io.modelcontextprotocol/clientCapabilities` is rejected with `-32602`. The spec requires it (see Appendix D of the book). Writing this example caught our own tiny client leaving it out; it's fixed now.
- **Tool specs stay the source of truth.** `toolFor` builds mcp-go tools from `toolspec.Spec` (schema, annotations, version, deprecation), so the registry that drives versioning, the gateway and the audit `tool_version` doesn't fork.
- **The idempotency key is this repo's own convention** (`co.kiosk/idempotencyKey` in `_meta`). MCP does not define one.
- **Separate Go module.** This folder has its own `go.mod` with a `replace` to the repository root, so the main module stays standard-library only.

## Not covered here

The same split (HTTP middleware for auth and limits, SDK hooks for the rest) should carry over to the official Go SDK ([modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk)), but it isn't tested here. This example targets mcp-go because a reader asked for it.
