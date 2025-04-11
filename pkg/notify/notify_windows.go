package notify

import (
	"fmt"
	"os"
	"strings"

	"git.sr.ht/~jackmordaunt/go-toast/v2"
	"github.com/google/uuid"
)

var (
	initialized = false
	appID       = getAppID()
	appGUID     = getGUID()
)

func getAppID() string {
	ex, err := os.Executable()
	if err != nil {
		return "deej"
	}
	return strings.ToLower(strings.ReplaceAll(ex, "\\", "-"))
}

// generate guid based on exe path
func getGUID() string {
	return uuid.NewSHA1(uuid.Nil, []byte(appID)).String()
}

func initalize(appIconPath, appName string) error {
	if initialized {
		return nil
	}

	// register app in registry
	// https://learn.microsoft.com/en-us/windows/apps/design/shell/tiles-and-notifications/send-local-toast-other-apps
	err := SetAppDataCustom(AppDataCustom{
		AppID:       appID,
		DisplayName: appName,
		GUID:        appGUID,
		IconPath:    appIconPath,
	})

	if err != nil {
		return err
	}

	initialized = true
	return nil
}

func Notify(title, message, appIconPath, appName string) error {
	err := initalize(appIconPath, appName)
	if err != nil {
		return fmt.Errorf("initialize toast: %w", err)
	}

	n := toast.Notification{
		AppID:    appID,
		Title:    title,
		Body:     message,
		Icon:     appIconPath,
		Duration: "short",
	}

	err = n.Push()
	if err != nil {
		return fmt.Errorf("push toast: %w", err)
	}

	return nil
}
