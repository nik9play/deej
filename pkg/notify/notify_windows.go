package notify

import (
	"github.com/go-toast/toast"
)

func Notify(title, message, appIconPath, appName string) error {
	notification := toast.Notification{
		AppID:   appName,
		Title:   title,
		Message: message,
		Icon:    appIconPath,
	}

	return notification.Push()
}
