package deej

import (
	"github.com/getlantern/systray"
	"github.com/nicksnyder/go-i18n/v2/i18n"

	"github.com/nik9play/deej/pkg/deej/icon"
	"github.com/nik9play/deej/pkg/deej/util"
)

func (d *Deej) initializeTray(onDone func()) {
	logger := d.logger.Named("tray")

	onReady := func() {
		logger.Debug("Tray instance ready")

		systray.SetTemplateIcon(icon.DeejLogo, icon.DeejLogo)
		systray.SetTitle("deej")
		systray.SetTooltip("deej")

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
		editConfig := systray.AddMenuItem(configTitle, configDescription)
		editConfig.SetIcon(icon.EditConfig)

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
		refreshSessions := systray.AddMenuItem(rescanTitle, rescanDescription)
		refreshSessions.SetIcon(icon.RefreshSessions)

		if d.version != "" {
			systray.AddSeparator()
			versionInfo := systray.AddMenuItem(d.version, "")
			versionInfo.Disable()
		}

		systray.AddSeparator()

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
		quit := systray.AddMenuItem(quitTitle, quitDescription)

		// wait on things to happen
		go func() {
			for {
				select {

				// quit
				case <-quit.ClickedCh:
					logger.Info("Quit menu item clicked, stopping")

					d.signalStop()

				// edit config
				case <-editConfig.ClickedCh:
					logger.Info("Edit config menu item clicked, opening config for editing")

					editor := "notepad.exe"
					if util.Linux() {
						editor = "gedit"
					}

					if err := util.OpenExternal(logger, editor, userConfigFilepath); err != nil {
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
