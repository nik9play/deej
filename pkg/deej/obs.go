package deej

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/andreykaipov/goobs"
	"github.com/andreykaipov/goobs/api/requests/inputs"
	"go.uber.org/zap"
)

type OBSClient struct {
	deej   *Deej
	logger *zap.SugaredLogger

	client *goobs.Client
	lock   sync.Mutex

	stopChannel chan struct{}
	errChannel  chan error
	wg          sync.WaitGroup

	// config values at time of connection
	hostConfig     string
	portConfig     int
	passwordConfig string
}

const (
	obsRetryDelay = 5 * time.Second
)

func NewOBSClient(deej *Deej, logger *zap.SugaredLogger) *OBSClient {
	logger = logger.Named("obs")

	o := &OBSClient{
		deej:       deej,
		logger:     logger,
		errChannel: make(chan error, 1),
	}

	logger.Debug("Created OBS client instance")

	o.setupOnConfigReload()

	return o
}

func (o *OBSClient) Start() {
	o.stopChannel = make(chan struct{})
	o.logger.Info("OBS client starting")

	go o.managerLoop()
}

func (o *OBSClient) Stop() {
	if o.stopChannel == nil {
		return
	}

	close(o.stopChannel)
	o.wg.Wait()

	o.logger.Info("OBS client stopped")
}

func (o *OBSClient) IsConnected() bool {
	o.lock.Lock()
	defer o.lock.Unlock()

	return o.client != nil
}

func (o *OBSClient) SetInputVolume(inputName string, volume float32) error {
	o.lock.Lock()
	defer o.lock.Unlock()

	if o.client == nil {
		return fmt.Errorf("not connected to OBS")
	}

	vol := float64(volume)
	_, err := o.client.Inputs.SetInputVolume(&inputs.SetInputVolumeParams{
		InputName:      &inputName,
		InputVolumeMul: &vol,
	})

	if err != nil {
		return err
	}

	o.logger.Debugw("Set OBS input volume", "input", inputName, "volume", volume)

	return nil
}

func (o *OBSClient) GetInputVolume(inputName string) (float32, error) {
	o.lock.Lock()
	defer o.lock.Unlock()

	if o.client == nil {
		return 0, fmt.Errorf("not connected to OBS")
	}

	resp, err := o.client.Inputs.GetInputVolume(&inputs.GetInputVolumeParams{
		InputName: &inputName,
	})

	if err != nil {
		return 0, err
	}

	return float32(resp.InputVolumeMul), nil
}

func (o *OBSClient) signalError(err error) {
	select {
	case o.errChannel <- err:
	default:
		// channel full, error already pending
	}
}

func (o *OBSClient) connect() error {
	o.lock.Lock()
	defer o.lock.Unlock()

	if o.client != nil {
		return fmt.Errorf("already connected")
	}

	cfg := o.deej.config.OBSConfig
	address := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	o.logger.Debugw("Attempting OBS connection", "address", address)

	opts := []goobs.Option{}
	if cfg.Password != "" {
		opts = append(opts, goobs.WithPassword(cfg.Password))
	}

	client, err := goobs.New(address, opts...)
	if err != nil {
		o.logger.Debugw("Failed to connect to OBS", "error", err)
		return fmt.Errorf("connect to OBS: %w", err)
	}

	o.client = client
	o.hostConfig = cfg.Host
	o.portConfig = cfg.Port
	o.passwordConfig = cfg.Password

	o.logger.Info("Connected to OBS")

	return nil
}

func (o *OBSClient) disconnect() {
	o.lock.Lock()
	defer o.lock.Unlock()

	if o.client == nil {
		return
	}

	_ = o.client.Disconnect()
	o.client = nil

	o.logger.Info("Disconnected from OBS")
}

func (o *OBSClient) managerLoop() {
	o.wg.Add(1)
	defer o.wg.Done()

	o.logger.Infow("Trying OBS connection",
		"host", o.deej.config.OBSConfig.Host,
		"port", o.deej.config.OBSConfig.Port,
	)

	for {
		// check if OBS is enabled
		if !o.deej.config.OBSConfig.Enabled {
			select {
			case <-o.stopChannel:
				o.logger.Debug("managerLoop: stop signal")
				return
			case <-time.After(obsRetryDelay):
				continue
			}
		}

		// attempt connection in goroutine so we can respond to stop signal
		connectResult := make(chan error, 1)
		go func() {
			connectResult <- o.connect()
		}()

		// wait for connection result or stop signal
		select {
		case <-o.stopChannel:
			o.logger.Debug("managerLoop: stop signal during connect")
			// wait for connect to finish, then disconnect if it succeeded
			if err := <-connectResult; err == nil {
				o.disconnect()
			}
			return

		case err := <-connectResult:
			if err != nil {
				o.logger.Debugw("OBS connection error, retrying...", "error", err)

				select {
				case <-o.stopChannel:
					o.logger.Debug("managerLoop: stop signal")
					return
				case <-time.After(obsRetryDelay):
					continue
				}
			}
		}

		// re-check if OBS was disabled while connecting
		if !o.deej.config.OBSConfig.Enabled {
			o.logger.Debug("OBS disabled while connecting, disconnecting")
			o.disconnect()
			continue
		}

		// drain any stale errors from previous connection
		select {
		case <-o.errChannel:
		default:
		}

		// start event listener to detect disconnection
		go o.eventLoop()

		select {
		case <-o.stopChannel:
			o.logger.Debug("managerLoop: stop signal")
			o.disconnect()
			return

		case err := <-o.errChannel:
			o.logger.Warnw("OBS connection error, reconnecting...", "error", err)
			o.disconnect()
			time.Sleep(obsRetryDelay)
			continue
		}
	}
}

func (o *OBSClient) eventLoop() {
	o.wg.Add(1)
	defer o.wg.Done()

	o.lock.Lock()
	client := o.client
	o.lock.Unlock()

	if client == nil {
		return
	}

	for {
		select {
		case <-o.stopChannel:
			return
		case _, ok := <-client.IncomingEvents:
			if !ok {
				// channel closed = disconnected
				o.signalError(errors.New("OBS connection closed"))
				return
			}
		}
	}
}

func (o *OBSClient) setupOnConfigReload() {
	configReloadedChannel := o.deej.config.SubscribeToChanges()

	go func() {
		for {
			<-configReloadedChannel

			// only trigger reconnect if currently connected
			if !o.IsConnected() {
				continue
			}

			cfg := o.deej.config.OBSConfig

			if cfg.Host != o.hostConfig ||
				cfg.Port != o.portConfig ||
				cfg.Password != o.passwordConfig ||
				!cfg.Enabled {

				o.logger.Debug("OBS config changed, triggering reconnect")
				o.signalError(errors.New("config changed"))
			}
		}
	}()
}
