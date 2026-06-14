package mcp

import (
	"context"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

// NewCommand builds the `mcp` subcommand. By default it speaks the MCP stdio
// transport (for an agent that launches the binary as a co-located
// subprocess). With --http it instead runs a long-lived Streamable HTTP server,
// the shape you deploy as an in-cluster Service that remote agents connect to.
//
// Connection details (server, credentials, TLS) come from the inherited
// persistent flags / env vars via the factories in Deps, so they are configured
// once at deploy time and never handled by the model.
func NewCommand(d Deps) *cobra.Command {
	var httpAddr string
	var httpPath string

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run an MCP server exposing broker operations as tools for AI agents",
		Long: `Starts a Model Context Protocol (MCP) server that exposes this broker's
messaging operations (send, request, peek, receive, ping, and management) as
tools an AI agent can call.

Transports:
  default   stdio (newline-delimited JSON on stdin/stdout)
  --http    Streamable HTTP server, e.g. --http ":8080" (path defaults to /mcp,
            with a /healthz probe for Kubernetes)

The broker connection is taken from the usual flags/environment
(--server/--user/--password and the *_SERVER/*_USER/*_PASSWORD variables), so
the agent only ever supplies message addresses and bodies.`,
		Args: cobra.NoArgs,
		// Connection is lazy (per tool call), so starting the server never
		// requires the broker to be reachable yet.
		RunE: func(c *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()

			srv := NewServerFromDeps(d)
			if httpAddr != "" {
				return srv.ServeHTTP(ctx, httpAddr, httpPath)
			}
			return srv.ServeStdio(ctx)
		},
	}

	cmd.Flags().StringVar(&httpAddr, "http", "", "Serve over Streamable HTTP on this address (e.g. \":8080\"); empty = stdio")
	cmd.Flags().StringVar(&httpPath, "http-path", "/mcp", "HTTP path for the MCP endpoint (used with --http)")

	return cmd
}
