package util

import (
	"errors"
	"os/exec"
)

func getCurrentWindowProcessNames(_ bool) ([]string, error) {
	return nil, errors.New("not implemented")
}

func getOpenExternalCommand(filename string) *exec.Cmd {
	return exec.Command("xdg-open", filename)
}

// do nothing
func getAutostartState() bool {
	return false
}

// do nothing
func setAutostartState(_ bool) error {
	return errors.New("not implemented")
}
