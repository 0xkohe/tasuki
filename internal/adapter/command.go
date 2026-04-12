package adapter

import "os/exec"

// commandContext creates an exec.Cmd with the given working directory.
func commandContext(name string, args []string, dir string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	return cmd
}
