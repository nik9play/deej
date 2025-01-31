package deej

import (
	"strconv"
	"strings"

	"github.com/getlantern/systray"
	"github.com/nicksnyder/go-i18n/v2/i18n"

	"github.com/nik9play/deej/pkg/deej/util"
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
	strs := make([]string, len(d.serial.currentSliderPercentValues))
	for i, num := range d.serial.currentSliderPercentValues {
		strs[i] = strconv.FormatFloat(float64(num)*100, 'f', 0, 32)
	}
	return strings.Join(strs, " | ")
}

func (d *Deej) initializeTray(onDone func()) {
	logger := d.logger.Named("tray")

	onReady := func() {
		logger.Debug("Tray instance ready")

		systray.SetTemplateIcon(DeejLogo, DeejLogo)

		systray.SetTitle("deej")

		setTooltip := func() {
			title := "deej\n" + getStatusItemTitle(d)
			if d.serial.GetState() {
				title += "\n" + getValuesString(d)
			}
			systray.SetTooltip(title)
		}
		setTooltip()

		configTitle, configDescription := getConfigItemText(d)
		editConfig := systray.AddMenuItem(configTitle, configDescription)
		editConfig.SetIcon(EditConfigIcon)

		rescanTitle, rescanDescription := getRescanItemText(d)
		refreshSessions := systray.AddMenuItem(rescanTitle, rescanDescription)
		refreshSessions.SetIcon(RefreshSessionsIcon)

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
