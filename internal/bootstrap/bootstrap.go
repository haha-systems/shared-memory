package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/log"
)

var lookPath = exec.LookPath

// Options control CLI bootstrap behavior.
type Options struct {
	ConfigPath string
	Scope      string
	ServerName string
	ServeCmd   string
	All        bool
	Codex      bool
	Claude     bool
	Gemini     bool
	DryRun     bool
}

// Command captures an executable command.
type Command struct {
	Name string
	Args []string
}

// Runner executes system commands.
type Runner interface {
	Run(name string, args ...string) error
}

// OSRunner executes commands via os/exec.
type OSRunner struct{}

func (OSRunner) Run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Bootstrap configures MCP servers for installed agent CLIs.
func Bootstrap(logger *log.Logger, opts Options, runner Runner) error {
	if runner == nil {
		runner = OSRunner{}
	}
	if opts.Scope == "" {
		opts.Scope = "user"
	}
	if opts.ServerName == "" {
		opts.ServerName = "shared-memory"
	}
	if strings.TrimSpace(opts.ServeCmd) == "" {
		opts.ServeCmd = "memory-mcp serve"
	}
	if !opts.All && !opts.Codex && !opts.Claude && !opts.Gemini {
		opts.All = true
	}

	cmds, err := BuildCommands(opts)
	if err != nil {
		return err
	}
	if len(cmds) == 0 {
		return errors.New("no bootstrap commands generated")
	}

	auditPath, err := auditLogPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(auditPath), 0o755); err != nil {
		return err
	}
	f, err := os.Create(auditPath)
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprintf(f, "# memory-mcp bootstrap %s\n", time.Now().UTC().Format(time.RFC3339))
	for _, c := range cmds {
		line := c.Name + " " + strings.Join(c.Args, " ")
		fmt.Fprintln(f, line)
		logger.Info("bootstrap command", "cmd", line, "dry_run", opts.DryRun)
		if opts.DryRun {
			continue
		}
		if err := runner.Run(c.Name, c.Args...); err != nil {
			// remove may fail when missing; ignore those to keep idempotency smooth.
			if strings.Contains(line, " mcp remove ") {
				logger.Debug("ignoring remove error", "cmd", line, "error", err)
				continue
			}
			return fmt.Errorf("run %q: %w", line, err)
		}
	}

	logger.Info("bootstrap complete", "audit_log", auditPath)
	return nil
}

// BuildCommands builds a deterministic bootstrap command list.
func BuildCommands(opts Options) ([]Command, error) {
	if opts.Scope != "user" && opts.Scope != "project" {
		return nil, fmt.Errorf("invalid scope %q (expected user or project)", opts.Scope)
	}
	if strings.TrimSpace(opts.ConfigPath) == "" {
		return nil, errors.New("config path is required")
	}
	if strings.TrimSpace(opts.ServeCmd) == "" {
		opts.ServeCmd = "memory-mcp serve"
	}

	cmdParts := strings.Fields(opts.ServeCmd)
	if len(cmdParts) == 0 {
		return nil, errors.New("serve command is required")
	}
	memoryCmd := append(cmdParts, "--config", opts.ConfigPath)
	cmds := make([]Command, 0, 8)

	addCodex := opts.All || opts.Codex
	addClaude := opts.All || opts.Claude
	addGemini := opts.All || opts.Gemini

	if addCodex && commandExists("codex") {
		cmds = append(cmds,
			Command{Name: "codex", Args: []string{"mcp", "remove", opts.ServerName}},
			Command{Name: "codex", Args: append([]string{"mcp", "add", opts.ServerName, "--"}, memoryCmd...)},
		)
	}
	if addClaude && commandExists("claude") {
		cmds = append(cmds,
			Command{Name: "claude", Args: []string{"mcp", "remove", "-s", opts.Scope, opts.ServerName}},
			Command{Name: "claude", Args: append([]string{"mcp", "add", "-s", opts.Scope, opts.ServerName, "--"}, memoryCmd...)},
		)
	}
	if addGemini && commandExists("gemini") {
		cmds = append(cmds,
			Command{Name: "gemini", Args: []string{"mcp", "remove", "-s", opts.Scope, opts.ServerName}},
			Command{Name: "gemini", Args: append([]string{"mcp", "add", "-s", opts.Scope, opts.ServerName}, memoryCmd...)},
		)
	}
	return cmds, nil
}

func commandExists(name string) bool {
	_, err := lookPath(name)
	return err == nil
}

func auditLogPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".memory-mcp", "bootstrap-last.log"), nil
}
