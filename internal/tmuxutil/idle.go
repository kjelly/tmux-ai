package tmuxutil

// ShellCommands lists process names that indicate a pane is idle (waiting
// at a shell prompt). This was previously duplicated in three places:
// internal/agent/runner.go, cmd/list.go, and cmd/popup.go.
var ShellCommands = map[string]bool{
	"bash": true, "zsh": true, "sh": true, "fish": true,
	"ksh": true, "csh": true, "nu": true, "tmux": true,
}

// IsIdle reports whether the given command name represents an idle shell.
func IsIdle(cmdName string) bool {
	return ShellCommands[cmdName]
}
