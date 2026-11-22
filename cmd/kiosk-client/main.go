// Command kiosk-client walks through the demo MCP server: auth, discovery,
// scoped tool lists, tenant isolation, validation and idempotent refunds.
//
//	terminal 1: go run ./cmd/kiosk-mcp
//	terminal 2: go run ./cmd/kiosk-client -token <token printed by the server>
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/AlwyAbdullah/the-tool-layer/internal/mcpclient"
)

func main() {
	url := flag.String("url", "http://127.0.0.1:8080/mcp", "MCP endpoint")
	token := flag.String("token", os.Getenv("KIOSK_TOKEN"), "bearer token (or set KIOSK_TOKEN)")
	flag.Parse()
	if *token == "" {
		fmt.Fprintln(os.Stderr, "missing -token: copy the dev token printed by go run ./cmd/kiosk-mcp")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := mcpclient.Walkthrough(ctx, os.Stdout, *url, *token); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
