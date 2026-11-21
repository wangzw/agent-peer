package node

import "sort"

// BaseEnv is the complete environment of every child process. Nothing is
// inherited from os.Environ: a test run from inside a Claude Code session would
// otherwise pass that session's variables, for example
// CLAUDE_CODE_MESSAGING_SOCKET, to the processes under test.
type BaseEnv struct {
	// Home becomes HOME.
	Home string
	// Path becomes PATH.
	Path string
	// Extra adds variables, for example PEERS_TEST_RUNTIME_ROOT.
	Extra map[string]string
}

// Build returns the environment as NAME=value strings, sorted by name. LANG is
// always C.UTF-8. A value of "" in more removes the variable, so a caller can
// drop one of the defaults or an extra.
func (e BaseEnv) Build(more map[string]string) []string {
	values := map[string]string{
		"HOME": e.Home,
		"PATH": e.Path,
		"LANG": "C.UTF-8",
	}
	for name, value := range e.Extra {
		setEnv(values, name, value)
	}
	for name, value := range more {
		setEnv(values, name, value)
	}

	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)

	env := make([]string, 0, len(names))
	for _, name := range names {
		env = append(env, name+"="+values[name])
	}
	return env
}

// setEnv sets name in values, or deletes it when value is empty.
func setEnv(values map[string]string, name, value string) {
	if value == "" {
		delete(values, name)
		return
	}
	values[name] = value
}
