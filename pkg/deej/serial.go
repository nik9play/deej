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

type VIDPID struct {
	VID uint64
	PID uint64
}

// SerialIO provides a deej-aware abstraction layer to managing serial I/O
type SerialIO struct {
	comPort  string
	baudRate int

	deej   *Deej
	logger *zap.SugaredLogger

	stopChannel chan struct{}
	errChannel  chan error
	wg          sync.WaitGroup
	stopping    bool
	port        serial.Port
	mode        serial.Mode

	lastKnownNumSliders        int
	currentSliderPercentValues []float32

	sliderMoveConsumers []chan SliderMoveEvent
}

var ErrNoSerialPorts = errors.New("no serial ports found")
var ErrAutoPortNotFound = errors.New("can't autodetect com port")

var allowedVIDPIDs = []VIDPID{{0x1A86, 0x7523}}

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
		deej:                deej,
		logger:              logger,
		port:                nil,
		errChannel:          make(chan error, 1),
		sliderMoveConsumers: []chan SliderMoveEvent{},
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

	sio.comPort = sio.deej.config.ConnectionInfo.COMPort
	sio.baudRate = sio.deej.config.ConnectionInfo.BaudRate

	if sio.comPort == "auto" {
		sio.logger.Infow("Trying to autodetect serial port")

		ports, err := enumerator.GetDetailedPortsList()

		if err != nil {
			sio.logger.Errorw("Failed to enumarate serial ports, retrying", "err", err)
			return ErrNoSerialPorts
		}
		if len(ports) == 0 {
			sio.logger.Warn("No serial ports found, retrying")
			return ErrNoSerialPorts
		}
		for _, port := range ports {
			sio.logger.Debugf("Found port: %s", port.Name)
			if port.IsUSB {
				sio.logger.Debugf("   USB ID     %s:%s", port.VID, port.PID)

				vid, _ := strconv.ParseUint(port.VID, 16, 16)
				pid, _ := strconv.ParseUint(port.PID, 16, 16)

				// Find Arduino Nano (CH340)
				for _, vidpid := range allowedVIDPIDs {
					if vid == vidpid.VID && pid == vidpid.PID {
						sio.logger.Infow("Found COM port", "com", port.Name, "vid", port.VID, "pid", port.PID)

						sio.comPort = port.Name
						break
					}
				}
			}
		}

		if sio.comPort == "auto" {
			sio.logger.Warn("COM port not found, retrying")
			return ErrAutoPortNotFound
		}
	}

	sio.mode = serial.Mode{
		BaudRate: sio.deej.config.ConnectionInfo.BaudRate,
		DataBits: 8,
		StopBits: serial.OneStopBit,
	}

	sio.logger.Debugw("Attempting serial connection",
		"comPort", sio.comPort,
		"baudRate", sio.mode.BaudRate)

	port, err := serial.Open(sio.comPort, &sio.mode)

	if err != nil {
		// might need a user notification here, TBD
		sio.logger.Warnw("Failed to open serial connection", "error", err)
		return err
	}

	port.SetReadTimeout(time.Duration(3) * time.Second)

	sio.port = port

	return nil
}

// Start attempts to connect to our arduino chip
func (sio *SerialIO) Start() {
	sio.stopping = false
	sio.stopChannel = make(chan struct{})
	sio.logger.Info("Serial starting")
	sio.wg.Add(1)
	go sio.managerLoop()
}

// Stop signals us to shut down our serial connection, if one is active
func (sio *SerialIO) Stop() {
	sio.stopping = true
	close(sio.stopChannel)

	// Wait for all goroutines to finish
	sio.wg.Wait()
	// Close the port after all loops have stopped
	sio.closePort()
	sio.logger.Info("Serial stopped")
}

// SubscribeToSliderMoveEvents returns an unbuffered channel that receives
// a sliderMoveEvent struct every time a slider moves
func (sio *SerialIO) SubscribeToSliderMoveEvents() chan SliderMoveEvent {
	ch := make(chan SliderMoveEvent)
	sio.sliderMoveConsumers = append(sio.sliderMoveConsumers, ch)

	return ch
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
			if sio.deej.config.ConnectionInfo.COMPort != sio.comPort ||
				sio.deej.config.ConnectionInfo.BaudRate != sio.baudRate {

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
//
//go:generate goi18n extract -sourceLanguage en
func (sio *SerialIO) managerLoop() {
	defer sio.wg.Done()

	for {
		if sio.stopping {
			sio.logger.Debug("managerLoop: stop var")
			return
		}

		sio.logger.Infof("Trying serial connection %s (baud %d)", sio.comPort, sio.baudRate)
		err := sio.connect()
		if err != nil {
			sio.logger.Warnw("Serial connection error. Trying again... ", "err", err)
			time.Sleep(2 * time.Second)
			continue
		}

		namedLogger := sio.logger.Named(strings.ToLower(sio.comPort))
		namedLogger.Infow("Connected", "port", sio.port)

		connectedTitle := sio.deej.localizer.MustLocalize(&i18n.LocalizeConfig{
			DefaultMessage: &i18n.Message{
				ID:    "ComPortConnectedNotificationTitle",
				Other: "Connected to {{.ComPort}}.",
			},
			TemplateData: map[string]string{
				"ComPort": sio.comPort,
			},
		})
		connectedDescription := sio.deej.localizer.MustLocalize(&i18n.LocalizeConfig{
			DefaultMessage: &i18n.Message{
				ID:    "ComPortConnectedNotificationDescription",
				Other: "Succesfully connected to deej.",
			},
		})
		sio.deej.notifier.Notify(connectedTitle, connectedDescription)

		sio.wg.Add(1)
		go sio.readLoop(namedLogger)

		select {
		case err := <-sio.errChannel:
			sio.logger.Warnw("Read line error", "err", err)
			disconnectedTitle := sio.deej.localizer.MustLocalize(&i18n.LocalizeConfig{
				DefaultMessage: &i18n.Message{
					ID:    "ComPortDisconnectedNotificationTitle",
					Other: "Disconnected from {{.ComPort}} due to an error.",
				},
				TemplateData: map[string]string{
					"ComPort": sio.comPort,
				},
			})
			disconnectedDescription := sio.deej.localizer.MustLocalize(&i18n.LocalizeConfig{
				DefaultMessage: &i18n.Message{
					ID:    "ComPortDisconnectedNotificationDescription",
					Other: "Trying to reconnect.",
				},
			})
			sio.deej.notifier.Notify(disconnectedTitle, disconnectedDescription)

			sio.closePort()
			time.Sleep(2 * time.Second)
			continue

		case <-sio.stopChannel:
			sio.logger.Debug("managerLoop: stop signal")
			sio.stopping = true
			return
		}
	}
}

func (sio *SerialIO) readLoop(logger *zap.SugaredLogger) {
	defer sio.wg.Done()

	reader := bufio.NewReader(sio.port)
	for {
		select {
		case <-sio.stopChannel:
			logger.Debug("readLoop: stop signal")
			return
		default:
		}

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

func (sio *SerialIO) closePort() {
	if err := sio.port.Close(); err != nil {
		sio.logger.Warnw("Failed to close serial connection", "error", err)
	} else {
		sio.logger.Debug("Serial connection closed")
	}

	sio.port = nil
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
		sio.currentSliderPercentValues = make([]float32, numSliders)

		// reset everything to be an impossible value to force the slider move event later
		for idx := range sio.currentSliderPercentValues {
			sio.currentSliderPercentValues[idx] = -1.0
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
		if util.SignificantlyDifferent(sio.currentSliderPercentValues[sliderIdx], normalizedScalar, sio.deej.config.NoiseReductionLevel) {

			// if it does, update the saved value and create a move event
			sio.currentSliderPercentValues[sliderIdx] = normalizedScalar

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
