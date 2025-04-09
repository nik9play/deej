package util

import (
	"fmt"
	"math"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"go.uber.org/zap"
)

// OpenExternal opens a file using the default associated program
func OpenExternal(logger *zap.SugaredLogger, filename string) error {
	command := getOpenExternalCommand(filename)

	if err := command.Run(); err != nil {
		logger.Warnw("Failed to open file",
			"filename", filename,
			"error", err)
		return fmt.Errorf("open file proc: %w", err)
	}

	return nil
}

// EnsureDirExists creates the given directory path if it doesn't already exist
func EnsureDirExists(path string) error {
	if err := os.MkdirAll(path, os.ModePerm); err != nil {
		return fmt.Errorf("ensure directory exists (%s): %w", path, err)
	}

	return nil
}

// FileExists checks if a file exists and is not a directory before we
// try using it to prevent further errors.
func FileExists(filename string) bool {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false
	}
	return !info.IsDir()
}

// Linux returns true if we're running on Linux
func Linux() bool {
	return runtime.GOOS == "linux"
}

// SetupCloseHandler creates a 'listener' on a new goroutine which will notify the
// program if it receives an interrupt from the OS
func SetupCloseHandler() chan os.Signal {
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	return c
}

// GetCurrentWindowProcessNames returns the process names (including extension, if applicable)
// of the current foreground window. This includes child processes belonging to the window.
// This is currently only implemented for Windows
func GetCurrentWindowProcessNames(checkFullscreen bool) ([]string, error) {
	return getCurrentWindowProcessNames(checkFullscreen)
}

func GetAutostartState() bool {
	return getAutostartState()
}

func SetAutostartState(state bool) error {
	return setAutostartState(state)
}

// NormalizeScalar "trims" the given float32 to 2 points of precision (e.g. 0.15442 -> 0.15)
// This is used both for windows core audio volume levels and for cleaning up slider level values from serial
func NormalizeScalar(v float32) float32 {
	return float32(math.Round(float64(v)*100) / 100.0)
}

// SignificantlyDifferent returns true if there's a significant enough volume difference between two given values
func SignificantlyDifferent(oldValue int, newValue int, noiseReductionLevel string) bool {
	const (
		noiseReductionHigh = "high"
		noiseReductionLow  = "low"
		noiseReductionNone = "none"
	)

	// this threshold is solely responsible for dealing with hardware interference when
	// sliders are producing noisy values.
	var significantDifferenceThreshold int

	// choose our noise reduction level based on the config-provided value
	switch noiseReductionLevel {
	case noiseReductionHigh:
		significantDifferenceThreshold = 20
	case noiseReductionLow:
		significantDifferenceThreshold = 5
	case noiseReductionNone:
		significantDifferenceThreshold = 1
	default:
		significantDifferenceThreshold = 10
	}

	// lower the threshold for edges to snap to 1.0 and 0.0
	if (newValue < 10 || newValue > 1013) && significantDifferenceThreshold > 5 {
		significantDifferenceThreshold = 5
	}

	if AbsInt(oldValue-newValue) >= significantDifferenceThreshold {
		return true
	}

	// values are close enough to not warrant any action
	return false
}

func AbsInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
