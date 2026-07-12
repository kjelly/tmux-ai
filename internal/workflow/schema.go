package workflow

// Workflow is a YAML-defined sequence/DAG of tmux-ai steps.
type Workflow struct {
	Name        string            `yaml:"name"`
	Description string            `yaml:"description"`
	Params      map[string]string `yaml:"params"` // default values; override with --param key=value
	Steps       []Step            `yaml:"steps"`
}

// Step is one unit of work in a workflow.
type Step struct {
	Name string `yaml:"name"`
	Type string `yaml:"type"` // run | watch | shell | relay | broadcast

	// -- type: run / broadcast --
	Instruction string `yaml:"instruction"`

	// -- type: run / watch / relay --
	Pane    string `yaml:"pane"`    // alias or %ID or session:window
	Session string `yaml:"session"` // alternative to pane
	Window  string `yaml:"window"`  // used with session

	// -- type: watch --
	Pattern  string `yaml:"pattern"`
	Absent   bool   `yaml:"absent"`   // true: trigger when pattern is NOT found (wait for disappearance)
	Range    string `yaml:"range"`    // Python slice expr applied before matching, e.g. "[-5:]"
	Timeout  int    `yaml:"timeout"`  // seconds (default 300)
	Lines    int    `yaml:"lines"`    // lines to capture (default 50)
	Interval int    `yaml:"interval"` // poll interval ms (default 2000)

	// -- type: shell --
	Command string `yaml:"command"`

	// -- type: relay --
	From    string `yaml:"from"`    // source pane
	To      string `yaml:"to"`      // target pane
	Trigger string `yaml:"trigger"` // optional trigger pattern

	// -- type: broadcast --
	Panes   string `yaml:"panes"`   // comma-separated IDs/aliases
	Process string `yaml:"process"` // match by process name

	// -- type: launch --
	Cwd   string `yaml:"cwd"`   // working directory for the new window
	Input string `yaml:"input"` // text to send after ready pattern is matched

	// -- type: wait --
	Until string `yaml:"until"` // natural language time expression (e.g. "今晚七點", "in 10 minutes")

	// -- notify shorthand (any type) --
	Notify string `yaml:"notify"` // show this message on step completion

	// -- flow control --
	Depends     []string    `yaml:"depends"`      // step names this step waits for
	When        string      `yaml:"when"`         // always | on_success | on_failure | contains(steps.X.output,"text") | not_contains(...)
	ContextFrom interface{} `yaml:"context_from"` // string or []string — pass step output as agent context
	OnFailure   string      `yaml:"on_failure"`   // stop (default) | continue
}

// StepResult holds the outcome of a single executed step.
type StepResult struct {
	Name   string
	Status string // success | failed | skipped
	Output string // captured output (agent response or shell stdout)
	Err    error
}
