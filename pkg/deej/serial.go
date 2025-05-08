package deej

import (
	"bufio"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"go.uber.org/zap"

	"github.com/nik9play/deej/pkg/deej/util"
)

// SerialIO provides a deej-aware abstraction layer to managing serial I/O
type SerialIO struct {
	comPortConfig  string
	comPortToUse   string
	baudRateConfig int

	deej   *Deej
	logger *zap.SugaredLogger

	stopChannel chan struct{}
	errChannel  chan error
	wg          sync.WaitGroup
	port        serial.Port
	mode        serial.Mode

	lastKnownNumSliders int
	currentSliderValues []int

	sliderMoveConsumers  []chan SliderMoveEvent
	stateChangeConsumers []chan bool
}

var ErrNoSerialPorts = errors.New("no serial ports found")
var ErrAutoPortNotFound = errors.New("can't autodetect com port")

// var allowedVIDPIDs = []VIDPID{{0x1A86, 0x7523}}

// SliderMoveEvent represents a single slider move captured by deej
type SliderMoveEvent struct {
	SliderID     int
	PercentValue float32
}

var expectedLinePattern = regexp.MustCompile(`^\d{1,4}(\|\d{1,4})*\r\n$`)

// NewSerialIO creates a SerialIO instance that uses the provided deej
// instance's connection info to establish communications with the arduino chip
func NewSerialIO(deej *Deej, logger *zap.SugaredLogger) (*SerialIO, error) {
	logger = logger.Named("serial")

	sio := &SerialIO{
		deej:                 deej,
		logger:               logger,
		port:                 nil,
		errChannel:           make(chan error, 1),
		sliderMoveConsumers:  []chan SliderMoveEvent{},
		stateChangeConsumers: []chan bool{},
	}

	logger.Debug("Created serial i/o instance")

	// respond to config changes
	sio.setupOnConfigReload()

	return sio, nil
}

func (sio *SerialIO) connect() error {
	// don't allow multiple concurrent connections
	if sio.port != nil {
		sio.logger.Warn("Already connected, can't start another without closing first")
		return errors.New("serial: connection already active")
	}

	// sio.stopped = false

	sio.comPortConfig = sio.deej.config.ConnectionInfo.COMPort
	sio.baudRateConfig = sio.deej.config.ConnectionInfo.BaudRate

	sio.comPortToUse = sio.comPortConfig

	allowedVIDPID := sio.deej.config.AutoSearchVIDPID

	if sio.comPortConfig == "auto" {
		sio.logger.Debugw("Trying to autodetect serial port")

		ports, err := enumerator.GetDetailedPortsList()

		if err != nil {
			sio.logger.Errorw("Failed to enumarate serial ports, retrying", "err", err)
			return ErrNoSerialPorts
		}
		if len(ports) == 0 {
			sio.logger.Debug("No serial ports found, retrying")
			return ErrNoSerialPorts
		}
		for _, port := range ports {
			sio.logger.Debugf("Found port: %s", port.Name)
			if port.IsUSB {
				sio.logger.Debugf("   USB ID     %s:%s", port.VID, port.PID)

				vid, _ := strconv.ParseUint(port.VID, 16, 16)
				pid, _ := strconv.ParseUint(port.PID, 16, 16)

				if vid == allowedVIDPID.VID && pid == allowedVIDPID.PID {
					sio.logger.Debugw("Found COM port", "com", port.Name, "vid", port.VID, "pid", port.PID)

					sio.comPortToUse = port.Name
					break
				}

			}
		}

		if sio.comPortToUse == "auto" {
			sio.logger.Debug("COM port not found, retrying")
			return ErrAutoPortNotFound
		}
	}

	sio.mode = serial.Mode{
		BaudRate: sio.deej.config.ConnectionInfo.BaudRate,
		DataBits: 8,
		StopBits: serial.OneStopBit,
	}

	sio.logger.Debugw("Attempting serial connection",
		"comPort", sio.comPortToUse,
		"baudRate", sio.mode.BaudRate)

	port, err := serial.Open(sio.comPortToUse, &sio.mode)

	if err != nil {
		// might need a user notification here, TBD
		sio.logger.Debugw("Failed to open serial connection", "error", err)
		return fmt.Errorf("open serial connection: %w", err)
	}

	// actually, this sets timeout to 0x7FFFFFFE instead of 0xFFFFFFFE
	// to make serial chip work properly.
	// see https://github.com/arduino/serial-monitor/issues/112
	err = port.SetReadTimeout(serial.NoTimeout)
	if err != nil {
		sio.logger.Warnw("Failed to set read timeout", "error", err)
		return fmt.Errorf("set read timeout: %w", err)
	}

	sio.port = port

	return nil
}

func (sio *SerialIO) GetState() bool {
	return sio.port != nil
}

// Start attempts to connect to our arduino chip
func (sio *SerialIO) Start() {
	sio.stopChannel = make(chan struct{})
	sio.logger.Info("Serial starting")

	go sio.managerLoop()
}

// Stop signals us to shut down our serial connection, if one is active
func (sio *SerialIO) Stop() {
	close(sio.stopChannel)

	// Wait for all goroutines to finish
	sio.wg.Wait()

	sio.logger.Info("Serial stopped")
}

// SubscribeToSliderMoveEvents returns an unbuffered channel that receives
// a sliderMoveEvent struct every time a slider moves
func (sio *SerialIO) SubscribeToSliderMoveEvents() chan SliderMoveEvent {
	ch := make(chan SliderMoveEvent)
	sio.sliderMoveConsumers = append(sio.sliderMoveConsumers, ch)

	return ch
}

func (sio *SerialIO) SubscribeToStateChangeEvent() chan bool {
	ch := make(chan bool)
	sio.stateChangeConsumers = append(sio.stateChangeConsumers, ch)

	return ch
}

func (sio *SerialIO) sendStateChangeEvent(state bool) {
	for _, consumer := range sio.stateChangeConsumers {
		consumer <- state
	}
}

func (sio *SerialIO) setupOnConfigReload() {
	configReloadedChannel := sio.deej.config.SubscribeToChanges()

	go func() {
		for {
			<-configReloadedChannel

			// make any config reload unset our slider number to ensure process volumes are being re-set
			// (the next read line will emit SliderMoveEvent instances for all sliders)\
			// this needs to happen after a small delay, because the session map will also re-acquire sessions
			// whenever the config file is reloaded, and we don't want it to receive these move events while the map
			// is still cleared. this is kind of ugly, but shouldn't cause any issues
			go func() {
				time.Sleep(50 * time.Millisecond)
				sio.lastKnownNumSliders = 0
			}()

			// if connection params have changed, attempt to stop and start the connection
			if sio.deej.config.ConnectionInfo.COMPort != sio.comPortConfig ||
				sio.deej.config.ConnectionInfo.BaudRate != sio.baudRateConfig {

				sio.logger.Info("Detected change in connection parameters, attempting to renew connection")
				sio.Stop()

				// let the connection close
				time.Sleep(2 * time.Second)

				sio.Start()
			}
		}
	}()
}

// manages serial connection and retries
func (sio *SerialIO) managerLoop() {
	sio.wg.Add(1)
	defer sio.wg.Done()

	sio.logger.Infow("Trying serial connection",
		"port", sio.deej.config.ConnectionInfo.COMPort,
		"vid", fmt.Sprintf("%X", sio.deej.config.AutoSearchVIDPID.VID),
		"pid", fmt.Sprintf("%X", sio.deej.config.AutoSearchVIDPID.PID),
	)

	for {
		err := sio.connect()
		if err != nil {
			sio.logger.Debugw("Serial connection error. Trying again...", "err", err)

			select {
			case <-sio.stopChannel:
				sio.logger.Debug("managerLoop: stop signal")
				return
			case <-time.After(2 * time.Second):
				continue
			}
		}

		sio.sendStateChangeEvent(true)

		namedLogger := sio.logger.Named(strings.ToLower(sio.comPortToUse))
		namedLogger.Infow("Connected")

		connectedTitle := sio.deej.localizer.MustLocalize(&i18n.LocalizeConfig{
			DefaultMessage: &i18n.Message{
				ID:    "ComPortConnectedNotificationTitle",
				Other: "Connected to {{.ComPort}}.",
			},
			TemplateData: map[string]string{
				"ComPort": sio.comPortToUse,
			},
		})
		connectedDescription := sio.deej.localizer.MustLocalize(&i18n.LocalizeConfig{
			DefaultMessage: &i18n.Message{
				ID:    "ComPortConnectedNotificationDescription",
				Other: "Succesfully connected to deej.",
			},
		})
		sio.deej.notifier.Notify(connectedTitle, connectedDescription)

		go sio.readLoop(namedLogger)

		select {
		case err := <-sio.errChannel:
			sio.logger.Warnw("Read line error", "err", err)
			sio.logger.Warn("Closing serial port")

			disconnectedTitle := sio.deej.localizer.MustLocalize(&i18n.LocalizeConfig{
				DefaultMessage: &i18n.Message{
					ID:    "ComPortDisconnectedNotificationTitle",
					Other: "Disconnected from {{.ComPort}} due to an error.",
				},
				TemplateData: map[string]string{
					"ComPort": sio.comPortToUse,
				},
			})
			disconnectedDescription := sio.deej.localizer.MustLocalize(&i18n.LocalizeConfig{
				DefaultMessage: &i18n.Message{
					ID:    "ComPortDisconnectedNotificationDescription",
					Other: "Trying to reconnect.",
				},
			})
			sio.deej.notifier.Notify(disconnectedTitle, disconnectedDescription)

			_ = sio.closePort()
			time.Sleep(2 * time.Second)
			continue

		case <-sio.stopChannel:
			sio.logger.Debug("managerLoop: stop signal")
			_ = sio.closePort()
			return
		}
	}
}

func (sio *SerialIO) readLoop(logger *zap.SugaredLogger) {
	sio.wg.Add(1)
	defer sio.wg.Done()

	reader := bufio.NewReader(sio.port)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			sio.errChannel <- fmt.Errorf("read error: %w", err)
			return
		}

		if sio.deej.Verbose() {
			logger.Debugw("Read new line", "line", line)
		}

		sio.handleLine(logger, line)
	}
}

func (sio *SerialIO) closePort() error {
	if sio.port == nil {
		return fmt.Errorf("port is already closed")
	}

	if err := sio.port.Close(); err != nil {
		sio.logger.Warnw("Failed to close serial connection", "error", err)
		return fmt.Errorf("close serial connection: %w", err)
	}

	sio.logger.Info("Serial connection closed")
	sio.port = nil
	sio.sendStateChangeEvent(false)
	return nil
}

func (sio *SerialIO) handleLine(logger *zap.SugaredLogger, line string) {
	// this function receives an unsanitized line which is guaranteed to end with LF,
	// but most lines will end with CRLF. it may also have garbage instead of
	// deej-formatted values, so we must check for that! just ignore bad ones
	if !expectedLinePattern.MatchString(line) {
		return
	}

	// trim the suffix
	line = strings.TrimSuffix(line, "\r\n")

	// split on pipe (|), this gives a slice of numerical strings between "0" and "1023"
	splitLine := strings.Split(line, "|")
	numSliders := len(splitLine)

	// update our slider count, if needed - this will send slider move events for all
	if numSliders != sio.lastKnownNumSliders {
		logger.Infow("Detected sliders", "amount", numSliders)
		sio.lastKnownNumSliders = numSliders
		sio.currentSliderValues = make([]int, numSliders)

		// reset everything to be an impossible value to force the slider move event later
		for idx := range sio.currentSliderValues {
			sio.currentSliderValues[idx] = -1023
		}
	}

	// for each slider:
	moveEvents := []SliderMoveEvent{}
	for sliderIdx, stringValue := range splitLine {

		// convert string values to integers ("1023" -> 1023)
		number, _ := strconv.Atoi(stringValue)

		// turns out the first line could come out dirty sometimes (i.e. "4558|925|41|643|220")
		// so let's check the first number for correctness just in case
		if sliderIdx == 0 && number > 1023 {
			logger.Debugw("Got malformed line from serial, ignoring", "line", line)
			return
		}

		// map the value from raw to a "dirty" float between 0 and 1 (e.g. 0.15451...)
		dirtyFloat := float32(number) / 1023.0

		// normalize it to an actual volume scalar between 0.0 and 1.0 with 2 points of precision
		normalizedScalar := util.NormalizeScalar(dirtyFloat)

		// if sliders are inverted, take the complement of 1.0
		if sio.deej.config.InvertSliders {
			normalizedScalar = 1 - normalizedScalar
		}

		// check if it changes the desired state (could just be a jumpy raw slider value)
		if util.SignificantlyDifferent(sio.currentSliderValues[sliderIdx], number, sio.deej.config.NoiseReductionLevel) {

			// if it does, update the saved value and create a move event
			sio.currentSliderValues[sliderIdx] = number

			moveEvents = append(moveEvents, SliderMoveEvent{
				SliderID:     sliderIdx,
				PercentValue: normalizedScalar,
			})

			if sio.deej.Verbose() {
				logger.Debugw("Slider moved", "event", moveEvents[len(moveEvents)-1])
			}
		}
	}

	// deliver move events if there are any, towards all potential consumers
	if len(moveEvents) > 0 {
		for _, consumer := range sio.sliderMoveConsumers {
			for _, moveEvent := range moveEvents {
				consumer <- moveEvent
			}
		}
	}
}
