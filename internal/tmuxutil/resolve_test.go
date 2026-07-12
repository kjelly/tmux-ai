package tmuxutil

import (
	"testing"

	"github.com/llmore/tmux-ai/internal/config"
)

type fakeTmuxResolver struct {
	resolved map[string]string
}

func (f *fakeTmuxResolver) ResolveTarget(target string) (string, error) {
	if id, ok := f.resolved[target]; ok {
		return id, nil
	}
	return target, nil
}

func TestResolvePane(t *testing.T) {
	aliases := config.Aliases{"build": "%25"}
	fake := &fakeTmuxResolver{resolved: map[string]string{"%25": "%25"}}
	r := NewResolverWith(aliases, fake)

	if got := r.ResolvePane("build"); got != "%25" {
		t.Errorf("ResolvePane(build) = %q, want %%25", got)
	}
	if got := r.ResolvePane("%30"); got != "%30" {
		t.Errorf("ResolvePane(%%30) = %q, want %%30", got)
	}
	if got := r.ResolvePane(""); got != "" {
		t.Errorf("ResolvePane empty = %q, want empty", got)
	}
}

func TestResolvePaneFromFlags(t *testing.T) {
	aliases := config.Aliases{"mypane": "%42"}
	fake := &fakeTmuxResolver{resolved: map[string]string{"%42": "%42"}}
	r := NewResolverWith(aliases, fake)

	if got := r.ResolvePaneFromFlags("mypane", "", ""); got != "%42" {
		t.Errorf("pane flag: got %q, want %%42", got)
	}
	if got := r.ResolvePaneFromFlags("", "mysession", "3"); got != "mysession:3" {
		t.Errorf("session+window: got %q, want mysession:3", got)
	}
	if got := r.ResolvePaneFromFlags("", "mysession", ""); got != "mysession:" {
		t.Errorf("session only: got %q, want mysession:", got)
	}
	if got := r.ResolvePaneFromFlags("", "", ""); got != "" {
		t.Errorf("empty flags: got %q, want empty", got)
	}
}
