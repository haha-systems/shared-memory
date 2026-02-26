package bootstrap

import "testing"

func TestBuildCommands_ScopeValidation(t *testing.T) {
	t.Parallel()
	_, err := BuildCommands(Options{ConfigPath: "/tmp/cfg.yaml", Scope: "bad", All: true, ServerName: "shared-memory"})
	if err == nil {
		t.Fatal("expected invalid scope error")
	}
}

func TestBuildCommands_DeterministicWhenCLIsPresent(t *testing.T) {
	t.Parallel()
	orig := lookPath
	lookPath = func(name string) (string, error) { return "/bin/" + name, nil }
	defer func() { lookPath = orig }()

	cmds, err := BuildCommands(Options{
		ConfigPath: "/tmp/cfg.yaml",
		Scope:      "user",
		ServerName: "shared-memory",
		All:        true,
	})
	if err != nil {
		t.Fatalf("BuildCommands() error = %v", err)
	}
	if len(cmds) != 6 {
		t.Fatalf("expected 6 commands (remove+add for 3 CLIs), got %d", len(cmds))
	}
	if cmds[0].Name != "codex" || cmds[1].Name != "codex" {
		t.Fatalf("expected codex first, got %q %q", cmds[0].Name, cmds[1].Name)
	}
	if cmds[2].Name != "claude" || cmds[4].Name != "gemini" {
		t.Fatalf("unexpected command ordering")
	}
}
