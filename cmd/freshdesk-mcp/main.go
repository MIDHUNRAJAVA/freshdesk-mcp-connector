// Command freshdesk-mcp runs the read-only Freshdesk connector as an MCP
// server on standard input and output.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"freshdesk-mcp-connector/internal/audit"
	"freshdesk-mcp-connector/internal/config"
	"freshdesk-mcp-connector/internal/freshdesk"
	"freshdesk-mcp-connector/internal/mcp"
	"freshdesk-mcp-connector/internal/tools"
)

const version = "0.2.0"

func main() {
	printTools := flag.Bool("print-tools", false, "print the tool definitions as JSON and exit (no credentials needed)")
	flag.Parse()

	if *printTools {
		if err := writeDefinitions(os.Stdout); err != nil {
			log.Fatal(err)
		}
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "freshdesk-mcp:", err)
		os.Exit(1)
	}
}

func writeDefinitions(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{"tools": mcp.Definitions(tools.New(tools.Deps{}))})
}

func run() error {
	settings, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	// stdout carries the protocol, so every log line goes to stderr.
	logger := log.New(os.Stderr, "freshdesk-mcp ", log.LstdFlags)
	settings.Client.Logf = logger.Printf

	client, err := freshdesk.NewClient(settings.Client)
	if err != nil {
		return err
	}
	auditOut, closeAudit, err := openAudit(settings.AuditLog)
	if err != nil {
		return err
	}
	defer closeAudit()

	srv := &mcp.Server{
		Name: "freshdesk-mcp", Version: version, Instructions: tools.Instructions,
		Tools:          tools.New(tools.Deps{API: client, Mask: settings.Mask, MaxResultBytes: settings.MaxResultBytes}),
		Audit:          audit.New(auditOut),
		MaxResultBytes: settings.MaxResultBytes + 2000, // headroom over the tools' own cap
	}
	logger.Printf("starting; api=%s mask_pii=%v read_only=true", client.BaseURL(), settings.Mask.Enabled)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := srv.Serve(ctx, os.Stdin, os.Stdout); err != nil && ctx.Err() == nil {
		return err
	}
	return nil
}

// openAudit picks where audit lines go: stderr by default.
func openAudit(target string) (io.Writer, func(), error) {
	switch target {
	case "":
		return os.Stderr, func() {}, nil
	case "off":
		return nil, func() {}, nil
	}
	f, err := os.OpenFile(target, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot open audit log: %w", err)
	}
	return f, func() { f.Close() }, nil
}
