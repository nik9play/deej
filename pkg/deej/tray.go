package deej

import (
	"strconv"
	"strings"

	"fyne.io/systray"
	"github.com/nicksnyder/go-i18n/v2/i18n"

	"github.com/nik9play/deej/pkg/deej/util"
	"github.com/nik9play/deej/pkg/icon"
)

func getConfigItemText(d *Deej) (string, string) {
	configTitle := d.localizer.MustLocalize(&i18n.LocalizeConfig{
		DefaultMessage: &i18n.Message{
			ID:    "EditConfigTitle",
			Other: "Edit configuration",
		},
	})
	configDescription := d.localizer.MustLocalize(&i18n.LocalizeConfig{
		DefaultMessage: &i18n.Message{
			ID:    "EditConfigDescription",
			Other: "Open config file with notepad",
		},
	})

	return configTitle, configDescription
}

func getSettingsItemText(d *Deej) (string, string) {
	configTitle := d.localizer.MustLocalize(&i18n.LocalizeConfig{
		DefaultMessage: &i18n.Message{
			ID:    "SettingsTitle",
			Other: "Settings",
		},
	})
	configDescription := d.localizer.MustLocalize(&i18n.LocalizeConfig{
		DefaultMessage: &i18n.Message{
			ID:    "SettingsDescription",
			Other: "Settings",
		},
	})

	return configTitle, configDescription
}

func getAutostartItemText(d *Deej) (string, string) {
	configTitle := d.localizer.MustLocalize(&i18n.LocalizeConfig{
		DefaultMessage: &i18n.Message{
			ID:    "AutostartTitle",
			Other: "Run at startup",
		},
	})
	configDescription := d.localizer.MustLocalize(&i18n.LocalizeConfig{
		DefaultMessage: &i18n.Message{
			ID:    "AutostartDescription",
			Other: "deej will launch at startup",
		},
	})

	return configTitle, configDescription
}

func getRescanItemText(d *Deej) (string, string) {
	rescanTitle := d.localizer.MustLocalize(&i18n.LocalizeConfig{
		DefaultMessage: &i18n.Message{
			ID:    "RescanSessionsTitle",
			Other: "Re-scan audio sessions",
		},
	})
	rescanDescription := d.localizer.MustLocalize(&i18n.LocalizeConfig{
		DefaultMessage: &i18n.Message{
			ID:    "RescanSessionsDescription",
			Other: "Manually refresh audio sessions if something's stuck",
		},
	})

	return rescanTitle, rescanDescription
}

func getQuitItemText(d *Deej) (string, string) {
	quitTitle := d.localizer.MustLocalize(&i18n.LocalizeConfig{
		DefaultMessage: &i18n.Message{
			ID:    "QuitTitle",
			Other: "Quit",
		},
	})
	quitDescription := d.localizer.MustLocalize(&i18n.LocalizeConfig{
		DefaultMessage: &i18n.Message{
			ID:    "QuitDescription",
			Other: "Stop deej and quit",
		},
	})

	return quitTitle, quitDescription
}

func getStatusItemTitle(d *Deej) string {
	var title string

	if d.serial.GetState() {
		title = d.localizer.MustLocalize(&i18n.LocalizeConfig{
			DefaultMessage: &i18n.Message{
				ID:    "StatusTrueTitle",
				Other: "Connected to {{.ComPort}}",
			},
			TemplateData: map[string]string{
				"ComPort": d.serial.comPortToUse,
			},
		})
	} else {
		title = d.localizer.MustLocalize(&i18n.LocalizeConfig{
			DefaultMessage: &i18n.Message{
				ID:    "StatusFalseTitle",
				Other: "Waiting for device...",
			},
		})
	}

	return title
}

func getValuesString(d *Deej) string {
	strs := make([]string, len(d.serial.currentSliderValues))
	for i, num := range d.serial.currentSliderValues {
		strs[i] = strconv.FormatFloat((float64(num)/1023.0)*100, 'f', 0, 32)
	}
	return strings.Join(strs, " | ")
}

func (d *Deej) initializeTray(onDone func()) {
	logger := d.logger.Named("tray")

	onReady := func() {
		logger.Debug("Tray instance ready")

		systray.SetTemplateIcon(icon.DeejLogo, icon.DeejLogo)

		systray.SetTitle("deej")

		setTooltip := func() {
			title := "deej\n" + getStatusItemTitle(d)
			if d.serial.GetState() {
				title += "\n" + getValuesString(d)
			}
			systray.SetTooltip(title)
		}
		setTooltip()

		settingsTitle, settingsDescription := getSettingsItemText(d)
		settings := systray.AddMenuItem(settingsTitle, settingsDescription)
		settings.SetIcon(icon.EditConfigIcon)

		configTitle, configDescription := getConfigItemText(d)
		editConfig := settings.AddSubMenuItem(configTitle, configDescription)

		autostartTitle, autostartDescription := getAutostartItemText(d)
		autostart := settings.AddSubMenuItemCheckbox(autostartTitle, autostartDescription, util.GetAutostartState())

		if util.Linux() {
			autostart.Hide()
		}

		rescanTitle, rescanDescription := getRescanItemText(d)
		refreshSessions := systray.AddMenuItem(rescanTitle, rescanDescription)
		refreshSessions.SetIcon(icon.RefreshSessionsIcon)

		systray.AddSeparator()

		statusInfo := systray.AddMenuItem(getStatusItemTitle(d), "")
		statusInfo.Disable()

		valuesInfo := systray.AddMenuItem("...", "")
		valuesInfo.Disable()
		valuesInfo.Hide()

		setValuesInfo := func() {
			if d.serial.GetState() {
				valuesInfo.SetTitle(getValuesString(d))
				valuesInfo.Show()
			} else {
				valuesInfo.Hide()
			}
		}
		setValuesInfo()

		if d.version != "" {
			versionInfo := systray.AddMenuItem(d.version, "")
			versionInfo.Disable()
		}

		systray.AddSeparator()

		quitTitle, quitDescription := getQuitItemText(d)
		quit := systray.AddMenuItem(quitTitle, quitDescription)

		sliderMovedChannel := d.serial.SubscribeToSliderMoveEvents()
		stateChangeChannel := d.serial.SubscribeToStateChangeEvent()

		// wait on things to happen
		go func() {
			for {
				select {
				// slider moved
				case <-sliderMovedChannel:
					setTooltip()
					setValuesInfo()

				// connection state changed
				case <-stateChangeChannel:
					setTooltip()
					setValuesInfo()
					statusInfo.SetTitle(getStatusItemTitle(d))

				// quit
				case <-quit.ClickedCh:
					logger.Info("Quit menu item clicked, stopping")

					d.signalStop()

				// edit config
				case <-editConfig.ClickedCh:
					logger.Info("Edit config menu item clicked, opening config for editing")

					if err := util.OpenExternal(logger, d.config.configPath); err != nil {
						logger.Warnw("Failed to open config file for editing", "error", err)
					}

				case <-autostart.ClickedCh:
					util.SetAutostartState(!util.GetAutostartState())
					if util.GetAutostartState() {
						autostart.Check()
					} else {
						autostart.Uncheck()
					}

				// refresh sessions
				case <-refreshSessions.ClickedCh:
					logger.Info("Refresh sessions menu item clicked, triggering session map refresh")

					// performance: the reason that forcing a refresh here is okay is that users can't spam the
					// right-click -> select-this-option sequence at a rate that's meaningful to performance
					d.sessions.refreshSessions(true)
				}
			}
		}()

		// actually start the main runtime
		onDone()
	}

	onExit := func() {
		logger.Debug("Tray exited")
	}

	// start the tray icon
	logger.Debug("Running in tray")
	systray.Run(onReady, onExit)
}

func (d *Deej) stopTray() {
	d.logger.Debug("Quitting tray")
	systray.Quit()
}
