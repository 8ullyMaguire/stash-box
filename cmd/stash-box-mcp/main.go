// Command stash-box-mcp is a READ-ONLY MCP server for one stash-box instance (plan phase 13 slice 13a).
//
// It speaks MCP over stdio, which is what a client launches and pipes to, and it talks to the instance's
// own GraphQL API over HTTP with an API key. Nothing is exposed here that the key could not already read,
// and nothing new listens on the instance.
//
// Usage:
//
//	stash-box-mcp --url http://127.0.0.1:9998/graphql --api-key "$STASH_BOX_API_KEY"
//
// The key may also come from STASH_BOX_API_KEY. It is never printed, and a missing one is not an error
// here: the instance is the thing that decides whether an anonymous caller may read anything, and it will
// answer "not authorized" per call, which is a truer message than this program's guess.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/stashapp/stash-box/internal/mcp"
)

// version is set at build time, the way the instance sets its own:
// -X 'github.com/stashapp/stash-box/internal/mcp.version=...'
var version = "dev"

func main() {
	url := flag.String("url", envOr("STASH_BOX_URL", "http://127.0.0.1:9998/graphql"),
		"the instance's GraphQL endpoint")
	apiKey := flag.String("api-key", envOr("STASH_BOX_API_KEY", ""),
		"the API key to present (defaults to $STASH_BOX_API_KEY); never printed")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("stash-box-mcp " + version)
		return
	}

	mcp.Version = version

	// Ctrl-C ends the stream, which is how a client that stops reading is handled too: the scanner returns
	// and the process exits rather than lingering with a pipe nobody holds.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := mcp.NewClient(*url, *apiKey)
	if err := mcp.Serve(ctx, os.Stdin, os.Stdout, client); err != nil {
		// stderr, not stdout: stdout is the protocol, and a diagnostic written there would be parsed as a
		// message.
		fmt.Fprintln(os.Stderr, "stash-box-mcp: "+err.Error())
		os.Exit(1)
	}
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
