package mcpclient

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Walkthrough exercises the demo server the way the book's chapters do and
// prints what happens. It returns an error if the server misbehaves, so the
// same code doubles as an end-to-end check.
func Walkthrough(ctx context.Context, w io.Writer, url, token string) error {
	c := &Client{URL: url, Token: token}
	step := func(title string) { fmt.Fprintf(w, "\n── %s\n", title) }
	expect := func(ok bool, format string, args ...any) error {
		if !ok {
			return fmt.Errorf("walkthrough: "+format, args...)
		}
		return nil
	}

	step("1. no token → 401 with resource metadata (Chapter 4)")
	anon := &Client{URL: url}
	r, err := anon.Do(ctx, "tools/list", nil, nil)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "HTTP %d  WWW-Authenticate: %s\n", r.Status, r.Header.Get("WWW-Authenticate"))
	if err := expect(r.Status == 401, "want 401 without token, got %d", r.Status); err != nil {
		return err
	}

	step("2. server/discover (Chapter 2)")
	if r, err = c.Do(ctx, "server/discover", nil, nil); err != nil {
		return err
	}
	fmt.Fprintf(w, "versions %v · instructions %q\n", r.Result["supportedVersions"], r.Result["instructions"])
	if err := expect(r.Status == 200, "discover status %d", r.Status); err != nil {
		return err
	}

	step("3. tools/list, filtered by the token's scopes (Chapters 3, 8)")
	if r, err = c.Do(ctx, "tools/list", nil, nil); err != nil {
		return err
	}
	names := ToolNames(r)
	fmt.Fprintf(w, "tools: %s (cacheScope=%v)\n", strings.Join(names, ", "), r.Result["cacheScope"])
	if err := expect(len(names) >= 3, "want at least 3 tools, got %v", names); err != nil {
		return err
	}

	step("4. lookup_order on your own order (Chapter 5)")
	if r, err = c.CallTool(ctx, "lookup_order", map[string]any{"order_id": "K-1001"}, ""); err != nil {
		return err
	}
	text, isErr := Text(r)
	fmt.Fprintf(w, "%s\n", text)
	if err := expect(!isErr && strings.Contains(text, "K-1001"), "lookup failed: %s", text); err != nil {
		return err
	}

	step("5. another seller's order looks exactly like a missing one (Chapter 5)")
	if r, err = c.CallTool(ctx, "lookup_order", map[string]any{"order_id": "K-2001"}, ""); err != nil {
		return err
	}
	text, isErr = Text(r)
	fmt.Fprintf(w, "isError=%v  %s\n", isErr, text)
	if err := expect(isErr, "cross-tenant lookup must fail"); err != nil {
		return err
	}

	step("6. invalid arguments are a tool error the model can read (Chapter 3)")
	if r, err = c.CallTool(ctx, "refund_order", map[string]any{"order_id": "K-1001", "reason": "changed_mind"}, ""); err != nil {
		return err
	}
	text, isErr = Text(r)
	fmt.Fprintf(w, "isError=%v  %s\n", isErr, text)
	if err := expect(isErr, "invalid reason must be rejected"); err != nil {
		return err
	}

	step("7. refund_order twice with one idempotency key (Chapter 7)")
	args := map[string]any{"order_id": "K-1001", "reason": "damaged"}
	r1, err := c.CallTool(ctx, "refund_order", args, "kiko-run-77-step-3")
	if err != nil {
		return err
	}
	r2, err := c.CallTool(ctx, "refund_order", args, "kiko-run-77-step-3")
	if err != nil {
		return err
	}
	t1, e1 := Text(r1)
	t2, _ := Text(r2)
	fmt.Fprintf(w, "first:  %s\nsecond: %s\n", t1, t2)
	if err := expect(!e1 && t1 == t2, "retry should replay the first answer"); err != nil {
		return err
	}

	step("8. refund again with a new key: rate limit, then nothing left to refund (Chapters 6, 7)")
	for attempt := 1; ; attempt++ {
		if r, err = c.CallTool(ctx, "refund_order", args, "kiko-run-78-step-1"); err != nil {
			return err
		}
		if r.Error == nil || attempt == 3 {
			break
		}
		wait := time.Second
		if s, convErr := strconv.Atoi(r.Header.Get("Retry-After")); convErr == nil && s > 0 {
			wait = time.Duration(s) * time.Second
		}
		fmt.Fprintf(w, "HTTP %d  %v (refunds cost 5 of a 20-token burst) → waiting %s\n", r.Status, r.Error, wait)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	text, isErr = Text(r)
	fmt.Fprintf(w, "%s\n", text)
	if err := expect(r.Error == nil && !isErr && strings.Contains(text, "Nothing left"), "want 'nothing left', got %v %s", r.Error, text); err != nil {
		return err
	}

	step("9. unknown method")
	if r, err = c.Do(ctx, "resources/list", nil, nil); err != nil {
		return err
	}
	if r.Error != nil {
		fmt.Fprintf(w, "HTTP %d  %v\n", r.Status, r.Error)
	}
	fmt.Fprintln(w, "\nAll checks passed. The audit log (one JSON line per call) is on the server's stderr.")
	return nil
}
