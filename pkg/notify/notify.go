package notify

import (
	"os"
	"path/filepath"

	"go.uber.org/zap"
)

type Notifier interface {
	Notify(title string, message string)
}

type ToastNotifier struct {
	logger      *zap.SugaredLogger
	appIconPath string
}

func NewToastNotifier(logger *zap.SugaredLogger) (*ToastNotifier, error) {
	logger = logger.Named("notifier")
	tn := &ToastNotifier{logger: logger, appIconPath: filepath.Join(os.TempDir(), "deej.ico")}

	logger.Debug("Created toast notifier instance")

	return tn, nil
}

func (tn *ToastNotifier) Notify(title string, message string) {
	err := Notify(title, message, tn.appIconPath, "deej")

	if err != nil {
		tn.logger.Errorw("Failed to send toast notification", "error", err)
	}
}
