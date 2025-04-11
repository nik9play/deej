package notify

import (
	"os"
	"path/filepath"

	"github.com/nik9play/deej/pkg/deej/util"
	"github.com/nik9play/deej/pkg/icon"
	"go.uber.org/zap"
)

type Notifier interface {
	Notify(title string, message string)
}

type ToastNotifier struct {
	logger *zap.SugaredLogger
}

func NewToastNotifier(logger *zap.SugaredLogger) (*ToastNotifier, error) {
	logger = logger.Named("notifier")
	tn := &ToastNotifier{logger: logger}

	logger.Debug("Created toast notifier instance")

	return tn, nil
}

func (tn *ToastNotifier) Notify(title string, message string) {
	appIconPath := tn.createIconFile()
	err := Notify(title, message, appIconPath, "deej")

	if err != nil {
		tn.logger.Errorw("Failed to send toast notification", "error", err)
	}
}

func (tn *ToastNotifier) createIconFile() (appIconPath string) {
	fileName := "deej.ico"
	if util.Linux() {
		fileName = "deej.png"
	}

	appIconPath = filepath.Join(os.TempDir(), fileName)

	if !util.FileExists(appIconPath) {
		tn.logger.Debugw("Deej icon file missing, creating", "path", appIconPath)

		f, err := os.Create(appIconPath)
		if err != nil {
			tn.logger.Errorw("Failed to create toast notification icon", "error", err)
		}

		if _, err = f.Write(icon.DeejLogo); err != nil {
			tn.logger.Errorw("Failed to write toast notification icon", "error", err)
		}

		if err = f.Close(); err != nil {
			tn.logger.Errorw("Failed to close toast notification icon", "error", err)
		}
	}

	return
}
