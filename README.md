# The Tool Layer: companion code

[![CI](https://github.com/AlwyAbdullah/the-tool-layer/actions/workflows/ci.yml/badge.svg)](https://github.com/AlwyAbdullah/the-tool-layer/actions/workflows/ci.yml)
![Go 1.25](https://img.shields.io/badge/go-1.25-00ADD8)
![dependencies: none](https://img.shields.io/badge/dependencies-none-brightgreen)
![MCP 2026-07-28](https://img.shields.io/badge/MCP-2026--07--28-8b74ff)
![license: MIT](https://img.shields.io/badge/license-MIT-blue)

**A production-minded MCP tool server in plain Go.** OAuth-style audience and scope checks with RFC 9728 metadata, tenant isolation, cost-weighted rate limits and quotas, idempotent side effects, tool versioning and sunset, injection signals for untrusted text, one audit line per call, and a gateway that pins and quarantines upstream tools.

This is the companion repository for the book **[The Tool Layer: Building MCP Servers and Agent Tools That Survive Production](https://leanpub.com/the-tool-layer)**. Every Go snippet in the book is copied from here, and every package has tests. The book explains why each check exists and what breaks without it.

- **Go 1.25, standard library only.**
- **MCP revision `2026-07-28`** (checked November 2026): stateless requests with `_meta`, `server/discover`, `tools/list` (with `ttlMs` and `cacheScope`), `tools/call` (with `resultType`, `content`, `structuredContent`, `isError`), header and body validation (`MCP-Protocol-Version`, `Mcp-Method`, `Mcp-Name`), and the `-32020` / `-32022` error codes. A minimal legacy `initialize` answers `2025-11-25` clients.

## Quick start

```bash
git clone https://github.com/AlwyAbdullah/the-tool-layer
cd the-tool-layer
go test ./...                 # 12 packages + an end-to-end test of the real server
```

Run the server and talk to it:

```bash
# terminal 1: localhost only; prints a development token for seller-a
go run ./cmd/kiosk-mcp

# terminal 2
go run ./cmd/kiosk-client -token <token printed by the server>
```

The client walks through what the book covers, against the real HTTP server:

```text
── 1. no token → 401 with resource metadata (Chapter 4)
HTTP 401  WWW-Authenticate: Bearer resource_metadata="http://127.0.0.1:8080/.well-known/oauth-protected-resource/mcp"

── 3. tools/list, filtered by the token's scopes (Chapters 3, 8)
tools: issue_refund, lookup_order, refund_order, search_catalog (cacheScope=private)

── 5. another seller's order looks exactly like a missing one (Chapter 5)
isError=true  No order "K-2001" in this account. Check the ID with the user.

── 6. invalid arguments are a tool error the model can read (Chapter 3)
isError=true  reason must be one of: damaged, not_delivered, wrong_item

── 7. refund_order twice with one idempotency key (Chapter 7)
first:  Refunded 6400 USD on K-1001.
second: Refunded 6400 USD on K-1001.

── 8. refund again with a new key: rate limit, then nothing left to refund (Chapters 6, 7)
HTTP 429  rpc error -32010: rate limited (refunds cost 5 of a 20-token burst) → waiting 1s
Nothing left to refund on K-1001; no money moved.
```

The server writes one JSON audit line per call to stderr: tenant, subject, client, tool and version, an argument hash (never the arguments), outcome, duration and a W3C trace ID.

You can point any MCP client that speaks `2026-07-28` over Streamable HTTP at `http://127.0.0.1:8080/mcp` with the printed token as a bearer token.

### Using an SDK? The same checks on mcp-go

[`examples/mcp-go`](examples/mcp-go) runs the same server on [mark3labs/mcp-go](https://github.com/mark3labs/mcp-go). mcp-go handles the protocol, and the book's packages do the checks: HTTP middleware for auth, scope and the 429, an mcp-go tool filter for scope-aware `tools/list`, and an mcp-go tool middleware for quota, idempotency, deprecation and audit. The same end-to-end walkthrough passes against it. It is a separate Go module, so the main repository stays dependency-free.

## Package map

| Chapter | Package | What it shows |
|---|---|---|
| 1 · Tools are your new public API | [`internal/mcp`](internal/mcp), [`internal/kiosk`](internal/kiosk) | The tool server and Kiosk & Co's seller tools |
| 2 · MCP in one chapter | [`internal/jsonrpc`](internal/jsonrpc), [`internal/mcp`](internal/mcp) | JSON-RPC 2.0 envelope, MCP error codes, transport rules, discover/list/call |
| 3 · Designing tools models use correctly | [`internal/kiosk`](internal/kiosk), [`internal/toolspec`](internal/toolspec) | A narrow refund tool, strict decoding, closed enums, naming rules, annotations |
| 4 · AuthN and AuthZ | [`internal/auth`](internal/auth) | Issuer, audience, expiry and scope checks; scope hierarchy; 401/403 challenges; RFC 9728 metadata |
| 5 · Multi-tenant servers | [`internal/tenant`](internal/tenant) | Principal in context, tenant-partitioned store, "not found" across tenants |
| 6 · Rate limits, quotas and cost attribution | [`internal/ratelimit`](internal/ratelimit) | Token bucket per tenant and client, cost-weighted calls, daily quota, usage meter |
| 7 · Idempotency and side effects | [`internal/idem`](internal/idem), [`internal/kiosk`](internal/kiosk) | Scoped keys, argument fingerprints, replay, in-progress protection, natural idempotency |
| 8 · Versioning and deprecation | [`internal/toolspec`](internal/toolspec) | Versions, deprecation and sunset dates, replacement tools, definition fingerprints |
| 9 · Tool poisoning and the supply chain | [`internal/sanitize`](internal/sanitize), [`internal/kiosk`](internal/kiosk) | Signals for hidden instructions and invisible characters in seller-written text |
| 10 · Observability and incident response | [`internal/audit`](internal/audit) | One audit record per call, argument hashes, W3C trace IDs |
| 11 · A production MCP gateway | [`internal/gateway`](internal/gateway) | Namespacing, version pinning, quarantine and default-deny for upstream tools |
| All | [`cmd/kiosk-mcp`](cmd/kiosk-mcp), [`cmd/kiosk-client`](cmd/kiosk-client), [`internal/mcpclient`](internal/mcpclient) | Runnable server, a tiny client, and the end-to-end walkthrough |

## What this is not

- **Not a complete or certified MCP implementation.** Not implemented: resources, prompts, SSE response streams, `subscriptions/listen`, multi round-trip (`input_required`) results, pagination, extensions. Use an official SDK in production if it fits, and keep the checks shown here.
- **Not a real identity provider.** Tokens are a compact HMAC-signed format standing in for your IdP's JWTs. The checks (issuer, audience, expiry, scope, tenant) are the point, not the format. The signing key is `dev-only-key`; never deploy this as-is.
- The `co.kiosk/idempotencyKey` `_meta` key is this code's own convention; MCP does not define an idempotency key.

## The book

*The Tool Layer* is for backend engineers who expose tools to AI agents: 11 chapters, 144 pages, 9 diagrams, with an Interview Corner and a production checklist in every chapter. It covers what this code can't: the incidents behind each check, the trade-offs, and how to design an MCP gateway in a 45-minute interview.

- Leanpub: <https://leanpub.com/the-tool-layer>
- Gumroad: <https://6363630357281.gumroad.com/l/the-tool-layer>

Related: [Agents Never Sleep](https://github.com/AlwyAbdullah/agents-never-sleep) (the agent side) and [Models Make Things Up](https://github.com/AlwyAbdullah/models-make-things-up) (LLM features).

Found a bug or a spec mismatch? [Open an issue](https://github.com/AlwyAbdullah/the-tool-layer/issues).

## License

MIT. See [LICENSE](LICENSE). The book text and diagrams are not part of this license.
