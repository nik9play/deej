package notify

import (
	"github.com/go-toast/toast"
)

func Notify(title, message, appIconPath string) error {
	notification := toast.Notification{
		AppID:   "deej",
		Title:   title,
		Message: message,
		Icon:    appIconPath,
	}

	return notification.Push()
}
