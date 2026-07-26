package cmd

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// yamlCommandHint identifies the most distinctive top-level fields of each
// supported YAML document. It is intentionally conservative: shared preset
// fields alone are not enough to claim that a file belongs to a command.
func yamlCommandHint(data []byte, current string) string {
	var fields map[string]yaml.Node
	if err := yaml.Unmarshal(data, &fields); err != nil {
		return ""
	}

	kind, usage := "", ""
	switch {
	case fields["steps"].Kind != 0:
		kind = "workflow"
		usage = `tmux-ai workflow run <file.yaml> (or workflow validate <file.yaml>)`
	case fields["members"].Kind != 0:
		kind = "coordinate preset"
		usage = `tmux-ai coordinate --preset <file.yaml>`
	case hasAnyYAMLField(fields, "pane1", "pane2", "initial_pane1", "initial_pane2", "pane1_role", "pane2_role"):
		kind = "pingpong preset"
		usage = `tmux-ai pingpong --preset <file.yaml>`
	case hasAnyYAMLField(fields, "model", "host", "max_iterations", "timeout", "verbose"):
		kind = "global config"
		usage = `tmux-ai --config <file.yaml> <command>`
	}

	if kind == "" || kind == current {
		return ""
	}
	return fmt.Sprintf("hint: this YAML looks like a %s; use %s", kind, usage)
}

func hasAnyYAMLField(fields map[string]yaml.Node, names ...string) bool {
	for _, name := range names {
		if fields[name].Kind != 0 {
			return true
		}
	}
	return false
}

func appendYAMLCommandHint(err error, data []byte, current string) error {
	if err == nil {
		return nil
	}
	if hint := yamlCommandHint(data, current); hint != "" {
		return fmt.Errorf("%w (%s)", err, hint)
	}
	return err
}
