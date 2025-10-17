package deej

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	ole "github.com/go-ole/go-ole"
	wca "github.com/moutend/go-wca/pkg/wca"
	"go.uber.org/zap"
)

type SessionResult struct {
	Sessions []Session
	Err      error
}

type wcaSessionFinder struct {
	logger        *zap.SugaredLogger
	sessionLogger *zap.SugaredLogger

	eventCtx *ole.GUID // needed for some session actions to successfully notify other audio consumers

	// needed for device change notifications
	mmDeviceEnumerator      *wca.IMMDeviceEnumerator
	mmNotificationClient    *wca.IMMNotificationClient
	lastDefaultDeviceChange time.Time

	// our master input and output sessions
	masterOut *masterSession
	masterIn  *masterSession

	lock sync.Mutex

	reqChannel chan struct{}
	resChannel chan SessionResult

	workerCtx    context.Context
	workerCancel context.CancelFunc
}

const (

	// there's no real mystery here, it's just a random GUID
	myteriousGUID = "{1ec920a1-7db8-44ba-9779-e5d28ed9f330}"

	// the notification client will call this multiple times in quick succession based on the
	// default device's assigned media roles, so we need to filter out the extraneous calls
	minDefaultDeviceChangeThreshold = 100 * time.Millisecond

	// prefix for device sessions in logger
	deviceSessionFormat = "device.%s"
)

func newSessionFinder(logger *zap.SugaredLogger) (SessionFinder, error) {
	ctx, cancel := context.WithCancel(context.Background())

	sf := &wcaSessionFinder{
		logger:        logger.Named("session_finder"),
		sessionLogger: logger.Named("sessions"),
		eventCtx:      ole.NewGUID(myteriousGUID),
		reqChannel:    make(chan struct{}),
		resChannel:    make(chan SessionResult),
		workerCtx:     ctx,
		workerCancel:  cancel,
	}

	sf.logger.Debug("Created WCA session finder instance")

	go sf.sessionFinderWorker(ctx)

	return sf, nil
}

// I noticed that deej crashes with E_INVALIDARG when trying to run it in VM over RDP
// It turned out that the problem was an incorrectly passed ctx argument in the go-wca library
func mmdActivateWorkaround(mmd *wca.IMMDevice, refIID *ole.GUID, ctx uint32, prop, obj interface{}) (err error) {
	objValue := reflect.ValueOf(obj).Elem()
	hr, _, _ := syscall.SyscallN(
		mmd.VTable().Activate,
		uintptr(unsafe.Pointer(mmd)),
		uintptr(unsafe.Pointer(refIID)),
		// uintptr(unsafe.Pointer(&ctx)),
		uintptr(ctx), // there is a fix
		0,
		objValue.Addr().Pointer())
	if hr != 0 {
		err = ole.NewError(hr)
	}
	return
}

func (sf *wcaSessionFinder) initializeCOMLoop(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			sf.logger.Info("COM initializing stopping")
			return errors.New("com initializing stopped")
		default:
			err := ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED)

			if err == nil {
				return nil
			}

			// if the error is "Incorrect function" that corresponds to 0x00000001,
			// which represents E_FALSE in COM error handling. this is fine for this function,
			// and just means that the call was redundant.
			const eFalse = 1
			oleError := &ole.OleError{}

			if errors.As(err, &oleError) {
				if oleError.Code() == eFalse {
					return nil
				} else {
					sf.logger.Warnw("Failed to call CoInitializeEx. Retrying...",
						"isOleError", true,
						"error", err,
						"oleError", oleError)

					time.Sleep(2 * time.Second)
				}
			} else {
				sf.logger.Warnw("Failed to call CoInitializeEx. Retrying...",
					"isOleError", false,
					"error", err,
					"oleError", nil)

				time.Sleep(2 * time.Second)
			}
		}
	}
}

// maybe initalizing COM is needed only once, so we do it in a dedicated goroutine
// that will also handle all session finding requests
// this way we avoid initializing and uninitializing COM multiple times
func (sf *wcaSessionFinder) sessionFinderWorker(ctx context.Context) {
	// seems like all COM operations must happen on the same initialized thread
	// it works fine without the lock, but i leave it here for the time being
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	if err := sf.initializeCOMLoop(ctx); err != nil {
		return
	}
	sf.logger.Info("COM initialized for session finder")
	defer ole.CoUninitialize()

	sf.logger.Debug("Starting COM message loop for session finder")

	for {
		select {
		case <-ctx.Done():
			sf.logger.Info("Session finder worker stopping")
			return
		case <-sf.reqChannel:
			start := time.Now()

			sessions, err := sf.getAllSessions()
			sf.resChannel <- SessionResult{Sessions: sessions, Err: err}

			elapsed := time.Since(start)
			sf.logger.Debugf("Get all sessions took %s", elapsed)
		}
	}
}

func (sf *wcaSessionFinder) getAllSessions() ([]Session, error) {
	sessions := []Session{}

	// ensure we have a device enumerator
	if err := sf.getDeviceEnumerator(); err != nil {
		sf.logger.Warnw("Failed to get device enumerator", "error", err)
		return nil, fmt.Errorf("get device enumerator: %w", err)
	}

	// get the currently active default output and input devices.
	// please note that this can return a nil defaultInputEndpoint, in case there are no input devices connected.
	// you must check it for non-nil
	defaultOutputEndpoint, defaultInputEndpoint, err := sf.getDefaultAudioEndpoints()
	if err != nil {
		sf.logger.Warnw("Failed to get default audio endpoints", "error", err)
		return nil, fmt.Errorf("get default audio endpoints: %w", err)
	}
	defer defaultOutputEndpoint.Release()

	if defaultInputEndpoint != nil {
		defer defaultInputEndpoint.Release()
	}

	// receive notifications whenever the default device changes (only do this once)
	if sf.mmNotificationClient == nil {
		if err := sf.registerDefaultDeviceChangeCallback(); err != nil {
			sf.logger.Warnw("Failed to register default device change callback", "error", err)
			return nil, fmt.Errorf("register default device change callback: %w", err)
		}
	}

	// get the master output session
	sf.masterOut, err = sf.getMasterSession(defaultOutputEndpoint, masterSessionName, masterSessionName)
	if err != nil {
		sf.logger.Warnw("Failed to get master audio output session", "error", err)
		return nil, fmt.Errorf("get master audio output session: %w", err)
	}

	sessions = append(sessions, sf.masterOut)

	// get the master input session, if a default input device exists
	if defaultInputEndpoint != nil {
		sf.masterIn, err = sf.getMasterSession(defaultInputEndpoint, inputSessionName, inputSessionName)
		if err != nil {
			sf.logger.Warnw("Failed to get master audio input session", "error", err)
			return nil, fmt.Errorf("get master audio input session: %w", err)
		}

		sessions = append(sessions, sf.masterIn)
	}

	// enumerate all devices and make their "master" sessions bindable by friendly name;
	// for output devices, this is also where we enumerate process sessions
	if err := sf.enumerateAndAddSessions(&sessions); err != nil {
		sf.logger.Warnw("Failed to enumerate device sessions", "error", err)
		return nil, fmt.Errorf("enumerate device sessions: %w", err)
	}
	return sessions, nil
}

func (sf *wcaSessionFinder) GetAllSessions() ([]Session, error) {
	sf.lock.Lock()
	defer sf.lock.Unlock()

	sf.reqChannel <- struct{}{}
	result := <-sf.resChannel

	return result.Sessions, result.Err
}

func (sf *wcaSessionFinder) Release() error {

	// skip unregistering the mmnotificationclient, as it's not implemented in go-wca
	if sf.mmDeviceEnumerator != nil {
		sf.mmDeviceEnumerator.Release()
	}

	sf.workerCancel()
	// close(sf.reqChannel)
	// close(sf.resChannel)

	sf.logger.Debug("Released WCA session finder instance")

	return nil
}

func (sf *wcaSessionFinder) getDeviceEnumerator() error {

	// get the IMMDeviceEnumerator (only once)
	if sf.mmDeviceEnumerator == nil {
		if err := wca.CoCreateInstance(
			wca.CLSID_MMDeviceEnumerator,
			0,
			wca.CLSCTX_ALL,
			wca.IID_IMMDeviceEnumerator,
			&sf.mmDeviceEnumerator,
		); err != nil {
			sf.logger.Warnw("Failed to call CoCreateInstance", "error", err)
			return fmt.Errorf("call CoCreateInstance: %w", err)
		}
	}

	return nil
}

func (sf *wcaSessionFinder) getDefaultAudioEndpoints() (*wca.IMMDevice, *wca.IMMDevice, error) {

	// get the default audio endpoints as IMMDevice instances
	var mmOutDevice *wca.IMMDevice
	var mmInDevice *wca.IMMDevice

	if err := sf.mmDeviceEnumerator.GetDefaultAudioEndpoint(wca.ERender, wca.EConsole, &mmOutDevice); err != nil {
		sf.logger.Warnw("Failed to call GetDefaultAudioEndpoint (out)", "error", err)
		return nil, nil, fmt.Errorf("call GetDefaultAudioEndpoint (out): %w", err)
	}

	// allow this call to fail (not all users have a microphone connected)
	if err := sf.mmDeviceEnumerator.GetDefaultAudioEndpoint(wca.ECapture, wca.EConsole, &mmInDevice); err != nil {
		sf.logger.Warn("No default input device detected, proceeding without it (\"mic\" will not work)")
		mmInDevice = nil
	}

	return mmOutDevice, mmInDevice, nil
}

func (sf *wcaSessionFinder) registerDefaultDeviceChangeCallback() error {
	callback := wca.IMMNotificationClientCallback{
		OnDefaultDeviceChanged: sf.defaultDeviceChangedCallback,
	}

	sf.mmNotificationClient = wca.NewIMMNotificationClient(callback)

	if err := sf.mmDeviceEnumerator.RegisterEndpointNotificationCallback(sf.mmNotificationClient); err != nil {
		sf.logger.Warnw("Failed to call RegisterEndpointNotificationCallback", "error", err)
		return fmt.Errorf("call RegisterEndpointNotificationCallback: %w", err)
	}

	return nil
}

func (sf *wcaSessionFinder) getMasterSession(mmDevice *wca.IMMDevice, key string, loggerKey string) (*masterSession, error) {

	var audioEndpointVolume *wca.IAudioEndpointVolume

	if err := mmdActivateWorkaround(mmDevice, wca.IID_IAudioEndpointVolume, wca.CLSCTX_ALL, nil, &audioEndpointVolume); err != nil {
		sf.logger.Warnw("Failed to activate AudioEndpointVolume for master session", "error", err)
		return nil, fmt.Errorf("activate master session: %w", err)
	}

	// create the master session
	master, err := newMasterSession(sf.sessionLogger, audioEndpointVolume, sf.eventCtx, key, loggerKey)
	if err != nil {
		sf.logger.Warnw("Failed to create master session instance", "error", err)
		return nil, fmt.Errorf("create master session: %w", err)
	}

	return master, nil
}

func (sf *wcaSessionFinder) enumerateAndAddSessions(sessions *[]Session) error {

	// get list of devices
	var deviceCollection *wca.IMMDeviceCollection

	if err := sf.mmDeviceEnumerator.EnumAudioEndpoints(wca.EAll, wca.DEVICE_STATE_ACTIVE, &deviceCollection); err != nil {
		sf.logger.Warnw("Failed to enumerate active audio endpoints", "error", err)
		return fmt.Errorf("enumerate active audio endpoints: %w", err)
	}

	// check how many devices there are
	var deviceCount uint32

	if err := deviceCollection.GetCount(&deviceCount); err != nil {
		sf.logger.Warnw("Failed to get device count from device collection", "error", err)
		return fmt.Errorf("get device count from device collection: %w", err)
	}

	// for each device:
	for deviceIdx := uint32(0); deviceIdx < deviceCount; deviceIdx++ {

		// get its IMMDevice instance
		var endpoint *wca.IMMDevice

		if err := deviceCollection.Item(deviceIdx, &endpoint); err != nil {
			sf.logger.Warnw("Failed to get device from device collection",
				"deviceIdx", deviceIdx,
				"error", err)

			return fmt.Errorf("get device %d from device collection: %w", deviceIdx, err)
		}
		defer endpoint.Release()

		// get its IMMEndpoint instance to figure out if it's an output device (and we need to enumerate its process sessions later)
		dispatch, err := endpoint.QueryInterface(wca.IID_IMMEndpoint)
		if err != nil {
			sf.logger.Warnw("Failed to query IMMEndpoint for device",
				"deviceIdx", deviceIdx,
				"error", err)

			return fmt.Errorf("query device %d IMMEndpoint: %w", deviceIdx, err)
		}

		// get the device's property store
		var propertyStore *wca.IPropertyStore

		if err := endpoint.OpenPropertyStore(wca.STGM_READ, &propertyStore); err != nil {
			sf.logger.Warnw("Failed to open property store for endpoint",
				"deviceIdx", deviceIdx,
				"error", err)

			return fmt.Errorf("open endpoint %d property store: %w", deviceIdx, err)
		}
		defer propertyStore.Release()

		// query the property store for the device's description and friendly name
		value := &wca.PROPVARIANT{}

		if err := propertyStore.GetValue(&wca.PKEY_Device_DeviceDesc, value); err != nil {
			sf.logger.Warnw("Failed to get description for device",
				"deviceIdx", deviceIdx,
				"error", err)

			return fmt.Errorf("get device %d description: %w", deviceIdx, err)
		}

		// device description i.e. "Headphones"
		endpointDescription := strings.ToLower(value.String())

		if err := propertyStore.GetValue(&wca.PKEY_Device_FriendlyName, value); err != nil {
			sf.logger.Warnw("Failed to get friendly name for device",
				"deviceIdx", deviceIdx,
				"error", err)

			return fmt.Errorf("get device %d friendly name: %w", deviceIdx, err)
		}

		// device friendly name i.e. "Headphones (Realtek Audio)"
		endpointFriendlyName := value.String()

		// receive a useful object instead of our dispatch
		endpointType := (*wca.IMMEndpoint)(unsafe.Pointer(dispatch))
		defer endpointType.Release()

		var dataFlow uint32
		if err := endpointType.GetDataFlow(&dataFlow); err != nil {
			sf.logger.Warnw("Failed to get data flow for endpoint",
				"deviceIdx", deviceIdx,
				"error", err)

			return fmt.Errorf("get device %d data flow: %w", deviceIdx, err)
		}

		sf.logger.Debugw("Enumerated device info",
			"deviceIdx", deviceIdx,
			"deviceDescription", endpointDescription,
			"deviceFriendlyName", endpointFriendlyName,
			"dataFlow", dataFlow)

		// if the device is an output device, enumerate and add its per-process audio sessions
		if dataFlow == wca.ERender {
			if err := sf.enumerateAndAddProcessSessions(endpoint, endpointFriendlyName, sessions); err != nil {
				sf.logger.Warnw("Failed to enumerate and add process sessions for device",
					"deviceIdx", deviceIdx,
					"error", err)

				return fmt.Errorf("enumerate and add device %d process sessions: %w", deviceIdx, err)
			}
		}

		// for all devices (both input and output), add a named "master" session that can be addressed
		// by using the device's friendly name (as appears when the user left-clicks the speaker icon in the tray)
		newSession, err := sf.getMasterSession(endpoint,
			endpointFriendlyName,
			fmt.Sprintf(deviceSessionFormat, endpointDescription))

		if err != nil {
			sf.logger.Warnw("Failed to get master session for device",
				"deviceIdx", deviceIdx,
				"error", err)

			return fmt.Errorf("get device %d master session: %w", deviceIdx, err)
		}

		// add it to our slice
		*sessions = append(*sessions, newSession)
	}

	return nil
}

func (sf *wcaSessionFinder) enumerateAndAddProcessSessions(
	endpoint *wca.IMMDevice,
	endpointFriendlyName string,
	sessions *[]Session,
) error {

	sf.logger.Debugw("Enumerating and adding process sessions for audio output device",
		"deviceFriendlyName", endpointFriendlyName)

	// query the given IMMDevice's IAudioSessionManager2 interface
	var audioSessionManager2 *wca.IAudioSessionManager2

	if err := mmdActivateWorkaround(
		endpoint,
		wca.IID_IAudioSessionManager2,
		wca.CLSCTX_ALL,
		nil,
		&audioSessionManager2,
	); err != nil {

		sf.logger.Warnw("Failed to activate endpoint as IAudioSessionManager2", "error", err)
		return fmt.Errorf("activate endpoint: %w", err)
	}
	defer audioSessionManager2.Release()

	// get its IAudioSessionEnumerator
	var sessionEnumerator *wca.IAudioSessionEnumerator

	if err := audioSessionManager2.GetSessionEnumerator(&sessionEnumerator); err != nil {
		return err
	}
	defer sessionEnumerator.Release()

	// check how many audio sessions there are
	var sessionCount int

	if err := sessionEnumerator.GetCount(&sessionCount); err != nil {
		sf.logger.Warnw("Failed to get session count from session enumerator", "error", err)
		return fmt.Errorf("get session count: %w", err)
	}

	sf.logger.Debugw("Got session count from session enumerator", "count", sessionCount)

	wg := sync.WaitGroup{}
	sm := sync.Mutex{}
	errCh := make(chan error, sessionCount)

	// for each session:
	for sessionIdx := 0; sessionIdx < sessionCount; sessionIdx++ {

		wg.Add(1)
		go func(sessionIdx int) {
			defer wg.Done()

			// get the IAudioSessionControl
			var audioSessionControl *wca.IAudioSessionControl
			if err := sessionEnumerator.GetSession(sessionIdx, &audioSessionControl); err != nil {
				sf.logger.Warnw("Failed to get session from session enumerator",
					"error", err,
					"sessionIdx", sessionIdx)

				errCh <- fmt.Errorf("get session %d from enumerator: %w", sessionIdx, err)
				return
			}

			// query its IAudioSessionControl2
			dispatch, err := audioSessionControl.QueryInterface(wca.IID_IAudioSessionControl2)
			if err != nil {
				sf.logger.Warnw("Failed to query session's IAudioSessionControl2",
					"error", err,
					"sessionIdx", sessionIdx)

				errCh <- fmt.Errorf("query session %d IAudioSessionControl2: %w", sessionIdx, err)
				return
			}

			// we no longer need the IAudioSessionControl, release it
			audioSessionControl.Release()

			// receive a useful object instead of our dispatch
			audioSessionControl2 := (*wca.IAudioSessionControl2)(unsafe.Pointer(dispatch))

			var pid uint32

			// get the session's PID
			if err := audioSessionControl2.GetProcessId(&pid); err != nil {

				// if this is the system sounds session, GetProcessId will error with an undocumented
				// AUDCLNT_S_NO_CURRENT_PROCESS (0x889000D) - this is fine, we actually want to treat it a bit differently
				// The first part of this condition will be true if the call to IsSystemSoundsSession fails
				// The second part will be true if the original error mesage from GetProcessId doesn't contain this magical
				// error code (in decimal format).
				isSystemSoundsErr := audioSessionControl2.IsSystemSoundsSession()
				if isSystemSoundsErr != nil && !strings.Contains(err.Error(), "143196173") {

					// of course, if it's not the system sounds session, we got a problem
					sf.logger.Warnw("Failed to query session's pid",
						"error", err,
						"isSystemSoundsError", isSystemSoundsErr,
						"sessionIdx", sessionIdx)

					errCh <- fmt.Errorf("query session %d pid: %w", sessionIdx, err)
					return
				}

				// update 2020/08/31: this is also the exact case for UWP applications, so we should no longer override the PID.
				// it will successfully update whenever we call GetProcessId for e.g. Video.UI.exe, despite the error being non-nil.
			}

			// get its ISimpleAudioVolume
			dispatch, err = audioSessionControl2.QueryInterface(wca.IID_ISimpleAudioVolume)
			if err != nil {
				sf.logger.Warnw("Failed to query session's ISimpleAudioVolume",
					"error", err,
					"sessionIdx", sessionIdx)

				errCh <- fmt.Errorf("query session %d ISimpleAudioVolume: %w", sessionIdx, err)
				return
			}

			// make it useful, again
			simpleAudioVolume := (*wca.ISimpleAudioVolume)(unsafe.Pointer(dispatch))

			// create the deej session object
			newSession, err := newWCASession(sf.sessionLogger, audioSessionControl2, simpleAudioVolume, pid, sf.eventCtx)
			if err != nil {

				// this could just mean this process is already closed by now, and the session will be cleaned up later by the OS
				if !errors.Is(err, errNoSuchProcess) {
					sf.logger.Warnw("Failed to create new WCA session instance",
						"error", err,
						"sessionIdx", sessionIdx)

					errCh <- fmt.Errorf("create wca session for session %d: %w", sessionIdx, err)
					return
				}

				// in this case, log it and release the session's handles, then skip to the next one
				sf.logger.Debugw("Process already exited, skipping session and releasing handles", "pid", pid)

				audioSessionControl2.Release()
				simpleAudioVolume.Release()

				return
			}

			// add it to our slice
			sm.Lock()
			*sessions = append(*sessions, newSession)
			sm.Unlock()

		}(sessionIdx)

	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		return err
	}

	return nil
}

//nolint:revive
func (sf *wcaSessionFinder) defaultDeviceChangedCallback(
	flow wca.EDataFlow, role wca.ERole, pwstrDeviceId string,
) error {

	// filter out calls that happen in rapid succession
	now := time.Now()

	if sf.lastDefaultDeviceChange.Add(minDefaultDeviceChangeThreshold).After(now) {
		return nil
	}

	sf.lastDefaultDeviceChange = now

	sf.logger.Debug("Default audio device changed, marking master sessions as stale")
	if sf.masterOut != nil {
		sf.masterOut.markAsStale()
	}

	if sf.masterIn != nil {
		sf.masterIn.markAsStale()
	}

	return nil
}
