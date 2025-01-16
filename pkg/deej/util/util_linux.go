package util

import (
	"errors"
	"fmt"
	"os/exec"

	"go.uber.org/zap"
)

func getCurrentWindowProcessNames() ([]string, error) {
	return nil, errors.New("Not implemented")
}

func OpenExternal(logger *zap.SugaredLogger, filename string) error {
	command := exec.Command("xdg-open", filename)

	if err := command.Run(); err != nil {
		logger.Warnw("Failed to open file",
			"filename", filename,
			"error", err)

		return fmt.Errorf("open file proc: %w", err)
	}

	return nil
}
