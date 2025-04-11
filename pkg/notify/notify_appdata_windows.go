package notify

import (
	"fmt"
	"path/filepath"
	"sync"

	"git.sr.ht/~jackmordaunt/go-toast/v2/wintoast"
	"github.com/go-ole/go-ole"
	"golang.org/x/sys/windows/registry"
)

/*
  This is an almost complete clone of the registy.go file from the git.sr.ht/~jackmordaunt/go-toast/v2/wintoast package.
  It is needed to add the ability to set DisplayName and AppID separately.
*/

var (
	appData   AppDataCustom
	appDataMu sync.Mutex
)

var (
	appKeyRoot    = filepath.Join("SOFTWARE", "Classes", "AppUserModelId")
	activationKey = getActivationKey()
)

type AppDataCustom struct {
	AppID               string
	DisplayName         string
	GUID                string
	ActivationExe       string // optional
	IconPath            string // optional
	IconBackgroundColor string // optional
}

func getActivationKey() string {
	return filepath.Join("SOFTWARE", "Classes", "CLSID", wintoast.GUID_ImplNotificationActivationCallback.String(), "LocalServer32")
}

func SetAppDataCustom(data AppDataCustom) (err error) {
	appDataMu.Lock()
	defer appDataMu.Unlock()

	// Early out if we have already set this data.
	//
	// In the case the data is empty, we don't want to overrite
	// all of the registry entries to empty.
	//
	// This allows the caller to either globally set the app data
	// or provide it per notification.
	if appData == data || data.AppID == "" {
		return nil
	}

	if data.GUID != "" {
		wintoast.GUID_ImplNotificationActivationCallback = ole.NewGUID(data.GUID)
		activationKey = getActivationKey()
	}

	// Keep a copy of the saved data for later.
	defer func() {
		if err == nil {
			appData = data
		}
	}()

	if err := setAppDataFunc(data); err != nil {
		return err
	}

	return nil
}

const registryDefaultKey string = ""

func setAppDataFunc(data AppDataCustom) error {
	if data.AppID == "" {
		return fmt.Errorf("empty app ID")
	}
	if data.DisplayName == "" {
		return fmt.Errorf("empty display name")
	}

	appKey := filepath.Join(appKeyRoot, data.AppID)

	if err := writeStringValue(appKey, "DisplayName", data.DisplayName); err != nil {
		return err
	}

	// CustomActivator teaches Window what COM class to use as the callback when
	// a toast notification is activated.
	if err := writeStringValue(appKey, "CustomActivator", wintoast.GUID_ImplNotificationActivationCallback.String()); err != nil {
		return err
	}

	if data.IconPath != "" {
		if err := writeStringValue(appKey, "IconUri", data.IconPath); err != nil {
			return err
		}
	}

	if data.IconBackgroundColor != "" {
		if err := writeStringValue(appKey, "IconBackgroundColor", data.IconBackgroundColor); err != nil {
			return err
		}
	}

	if data.ActivationExe != "" {
		if err := writeStringValue(activationKey, registryDefaultKey, data.ActivationExe); err != nil {
			return fmt.Errorf("setting activation executable: %w", err)
		}
	}

	return nil
}

// writeStringValue writes a string value to the path, where name is the subkey and
// value is the literal value.
func writeStringValue(path, name, value string) error {
	// always rewrite registry values
	// if keyExists(path, name) {
	// 	return nil
	// }
	key, _, err := registry.CreateKey(registry.CURRENT_USER, path, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("opening registry key: %s: %w", path, err)
	}
	if err := key.SetStringValue(name, value); err != nil {
		return fmt.Errorf("setting string value: (%s) %s=%s: %w", path, name, value, err)
	}
	if err := key.Close(); err != nil {
		return fmt.Errorf("closing key: %s: %w", path, err)
	}
	return nil
}

// keyExists returns true if the key exists.
func keyExists(path, name string) bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.READ)
	if err != nil {
		return false
	}
	defer key.Close()
	v, _, err := key.GetStringValue(name)
	if err != nil {
		return false
	}
	return v != ""
}
