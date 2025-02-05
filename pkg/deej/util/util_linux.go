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
