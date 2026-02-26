package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/charmbracelet/log"

	"github.com/xiy/memory-mcp/internal/admin"
	"github.com/xiy/memory-mcp/internal/bootstrap"
	"github.com/xiy/memory-mcp/internal/config"
	"github.com/xiy/memory-mcp/internal/mcp"
	"github.com/xiy/memory-mcp/internal/memory"
	"github.com/xiy/memory-mcp/internal/store"
	"github.com/xiy/memory-mcp/internal/ttl"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	sub := os.Args[1]
	switch sub {
	case "serve":
		if err := runServe(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "bootstrap-clis":
		if err := runBootstrap(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "admin":
		if err := runAdmin(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	case "version", "--version", "-v":
		fmt.Println("memory-mcp v0.1.0")
	default:
		usage()
		os.Exit(2)
	}
}

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := fs.String("config", "config/memory-mcp.yaml", "Path to config file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if err := cfg.EnsurePaths(); err != nil {
		return err
	}

	logger := log.NewWithOptions(os.Stderr, log.Options{ReportCaller: false, Prefix: cfg.ServerName})
	setLogLevel(logger, cfg.LogLevel)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	st, err := store.OpenSQLite(ctx, cfg.DBPath, logger)
	if err != nil {
		return err
	}
	defer st.Close()

	svc, err := memory.NewService(st, cfg, logger)
	if err != nil {
		return err
	}

	go ttl.Start(ctx, logger, time.Duration(cfg.TTLCheckIntervalSeconds)*time.Second, svc)

	server := mcp.NewServer(svc, logger, st)
	logger.Info("starting MCP stdio server", "db", cfg.DBPath)
	if err := server.Serve(ctx, os.Stdin, os.Stdout); err != nil && err != context.Canceled {
		return err
	}
	return nil
}

func runBootstrap(args []string) error {
	fs := flag.NewFlagSet("bootstrap-clis", flag.ContinueOnError)
	configPath := fs.String("config", "config/memory-mcp.yaml", "Path to config file")
	scope := fs.String("scope", "user", "Config scope: user or project")
	serverName := fs.String("server-name", "shared-memory", "MCP server registration name")
	serveCmd := fs.String("serve-command", "memory-mcp serve", "Command used by MCP clients to launch the stdio server")
	all := fs.Bool("all", false, "Configure all available CLIs")
	codex := fs.Bool("codex", false, "Configure Codex CLI")
	claude := fs.Bool("claude", false, "Configure Claude CLI")
	gemini := fs.Bool("gemini", false, "Configure Gemini CLI")
	dryRun := fs.Bool("dry-run", false, "Print intended commands without executing")
	if err := fs.Parse(args); err != nil {
		return err
	}

	logger := log.New(os.Stderr)
	return bootstrap.Bootstrap(logger, bootstrap.Options{
		ConfigPath: *configPath,
		Scope:      *scope,
		ServerName: *serverName,
		ServeCmd:   *serveCmd,
		All:        *all,
		Codex:      *codex,
		Claude:     *claude,
		Gemini:     *gemini,
		DryRun:     *dryRun,
	}, nil)
}

func runAdmin(args []string) error {
	fs := flag.NewFlagSet("admin", flag.ContinueOnError)
	configPath := fs.String("config", "config/memory-mcp.yaml", "Path to config file")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if err := cfg.EnsurePaths(); err != nil {
		return err
	}

	logger := log.New(os.Stderr)
	st, err := store.OpenSQLite(context.Background(), cfg.DBPath, logger)
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	return admin.Run(ctx, st)
}

func setLogLevel(logger *log.Logger, level string) {
	switch level {
	case "debug":
		logger.SetLevel(log.DebugLevel)
	case "warn":
		logger.SetLevel(log.WarnLevel)
	case "error":
		logger.SetLevel(log.ErrorLevel)
	default:
		logger.SetLevel(log.InfoLevel)
	}
}

func usage() {
	fmt.Print(`memory-mcp

Usage:
  memory-mcp serve [--config path]
  memory-mcp bootstrap-clis [--config path] [--all|--codex --claude --gemini] [--scope user|project]
  memory-mcp admin [--config path]
  memory-mcp version
`)
}
