package deej

import (
	"syscall"
	"unsafe"

	ole "github.com/go-ole/go-ole"
	wca "github.com/moutend/go-wca/pkg/wca"
)

// Audio session disconnect reasons from MSDN
const (
	DisconnectReasonDeviceRemoval         = 0
	DisconnectReasonServerShutdown        = 1
	DisconnectReasonFormatChanged         = 2
	DisconnectReasonSessionLogoff         = 3
	DisconnectReasonSessionDisconnected   = 4
	DisconnectReasonExclusiveModeOverride = 5
)

// IAudioSessionEventsCallback contains the callback functions for audio session events
type IAudioSessionEventsCallback struct {
	OnDisplayNameChanged   func(newDisplayName string, eventContext *ole.GUID) error
	OnIconPathChanged      func(newIconPath string, eventContext *ole.GUID) error
	OnSimpleVolumeChanged  func(newVolume float32, newMute bool, eventContext *ole.GUID) error
	OnChannelVolumeChanged func(channelCount uint32, newChannelVolumeArray uintptr, changedChannel uint32, eventContext *ole.GUID) error
	OnGroupingParamChanged func(newGroupingParam *ole.GUID, eventContext *ole.GUID) error
	OnStateChanged         func(newState uint32) error
	OnSessionDisconnected  func(disconnectReason uint32) error
}

// IAudioSessionEvents is a COM callback interface for audio session events
type IAudioSessionEvents struct {
	vTable   *iAudioSessionEventsVtbl
	refCount int
	callback IAudioSessionEventsCallback
}

type iAudioSessionEventsVtbl struct {
	ole.IUnknownVtbl
	OnDisplayNameChanged   uintptr
	OnIconPathChanged      uintptr
	OnSimpleVolumeChanged  uintptr
	OnChannelVolumeChanged uintptr
	OnGroupingParamChanged uintptr
	OnStateChanged         uintptr
	OnSessionDisconnected  uintptr
}

func aseQueryInterface(this uintptr, riid *ole.GUID, ppInterface *uintptr) int64 {
	*ppInterface = 0

	if ole.IsEqualGUID(riid, ole.IID_IUnknown) ||
		ole.IsEqualGUID(riid, wca.IID_IAudioSessionEvents) {
		aseAddRef(this)
		*ppInterface = this
		return ole.S_OK
	}

	return ole.E_NOINTERFACE
}

func aseAddRef(this uintptr) int64 {
	ase := (*IAudioSessionEvents)(unsafe.Pointer(this))
	ase.refCount++
	return int64(ase.refCount)
}

func aseRelease(this uintptr) int64 {
	ase := (*IAudioSessionEvents)(unsafe.Pointer(this))
	ase.refCount--
	return int64(ase.refCount)
}

func aseOnDisplayNameChanged(this uintptr, newDisplayName uintptr, eventContext uintptr) int64 {
	ase := (*IAudioSessionEvents)(unsafe.Pointer(this))

	if ase.callback.OnDisplayNameChanged == nil {
		return ole.S_OK
	}

	name := wca.LPCWSTRToString(newDisplayName, 1024)
	ctx := (*ole.GUID)(unsafe.Pointer(eventContext))

	if err := ase.callback.OnDisplayNameChanged(name, ctx); err != nil {
		return ole.E_FAIL
	}

	return ole.S_OK
}

func aseOnIconPathChanged(this uintptr, newIconPath uintptr, eventContext uintptr) int64 {
	ase := (*IAudioSessionEvents)(unsafe.Pointer(this))

	if ase.callback.OnIconPathChanged == nil {
		return ole.S_OK
	}

	path := wca.LPCWSTRToString(newIconPath, 1024)
	ctx := (*ole.GUID)(unsafe.Pointer(eventContext))

	if err := ase.callback.OnIconPathChanged(path, ctx); err != nil {
		return ole.E_FAIL
	}

	return ole.S_OK
}

func aseOnSimpleVolumeChanged(this uintptr, newVolume uintptr, newMute uintptr, eventContext uintptr) int64 {
	ase := (*IAudioSessionEvents)(unsafe.Pointer(this))

	if ase.callback.OnSimpleVolumeChanged == nil {
		return ole.S_OK
	}

	vol := *(*float32)(unsafe.Pointer(&newVolume))
	mute := newMute != 0
	ctx := (*ole.GUID)(unsafe.Pointer(eventContext))

	if err := ase.callback.OnSimpleVolumeChanged(vol, mute, ctx); err != nil {
		return ole.E_FAIL
	}

	return ole.S_OK
}

func aseOnChannelVolumeChanged(this uintptr, channelCount uintptr, newChannelVolumeArray uintptr, changedChannel uintptr, eventContext uintptr) int64 {
	ase := (*IAudioSessionEvents)(unsafe.Pointer(this))

	if ase.callback.OnChannelVolumeChanged == nil {
		return ole.S_OK
	}

	ctx := (*ole.GUID)(unsafe.Pointer(eventContext))

	if err := ase.callback.OnChannelVolumeChanged(uint32(channelCount), newChannelVolumeArray, uint32(changedChannel), ctx); err != nil {
		return ole.E_FAIL
	}

	return ole.S_OK
}

func aseOnGroupingParamChanged(this uintptr, newGroupingParam uintptr, eventContext uintptr) int64 {
	ase := (*IAudioSessionEvents)(unsafe.Pointer(this))

	if ase.callback.OnGroupingParamChanged == nil {
		return ole.S_OK
	}

	param := (*ole.GUID)(unsafe.Pointer(newGroupingParam))
	ctx := (*ole.GUID)(unsafe.Pointer(eventContext))

	if err := ase.callback.OnGroupingParamChanged(param, ctx); err != nil {
		return ole.E_FAIL
	}

	return ole.S_OK
}

func aseOnStateChanged(this uintptr, newState uintptr) int64 {
	ase := (*IAudioSessionEvents)(unsafe.Pointer(this))

	if ase.callback.OnStateChanged == nil {
		return ole.S_OK
	}

	if err := ase.callback.OnStateChanged(uint32(newState)); err != nil {
		return ole.E_FAIL
	}

	return ole.S_OK
}

func aseOnSessionDisconnected(this uintptr, disconnectReason uintptr) int64 {
	ase := (*IAudioSessionEvents)(unsafe.Pointer(this))

	if ase.callback.OnSessionDisconnected == nil {
		return ole.S_OK
	}

	if err := ase.callback.OnSessionDisconnected(uint32(disconnectReason)); err != nil {
		return ole.E_FAIL
	}

	return ole.S_OK
}

// NewIAudioSessionEvents creates a new IAudioSessionEvents callback interface
func NewIAudioSessionEvents(callback IAudioSessionEventsCallback) *IAudioSessionEvents {
	vTable := &iAudioSessionEventsVtbl{}

	// IUnknown methods
	vTable.QueryInterface = syscall.NewCallback(aseQueryInterface)
	vTable.AddRef = syscall.NewCallback(aseAddRef)
	vTable.Release = syscall.NewCallback(aseRelease)

	// IAudioSessionEvents methods
	vTable.OnDisplayNameChanged = syscall.NewCallback(aseOnDisplayNameChanged)
	vTable.OnIconPathChanged = syscall.NewCallback(aseOnIconPathChanged)
	vTable.OnSimpleVolumeChanged = syscall.NewCallback(aseOnSimpleVolumeChanged)
	vTable.OnChannelVolumeChanged = syscall.NewCallback(aseOnChannelVolumeChanged)
	vTable.OnGroupingParamChanged = syscall.NewCallback(aseOnGroupingParamChanged)
	vTable.OnStateChanged = syscall.NewCallback(aseOnStateChanged)
	vTable.OnSessionDisconnected = syscall.NewCallback(aseOnSessionDisconnected)

	ase := &IAudioSessionEvents{}
	ase.vTable = vTable
	ase.callback = callback

	return ase
}

// ToWCA returns the pointer cast to wca.IAudioSessionEvents for use with WCA functions
func (ase *IAudioSessionEvents) ToWCA() *wca.IAudioSessionEvents {
	return (*wca.IAudioSessionEvents)(unsafe.Pointer(ase))
}

// IAudioSessionNotificationCallback contains the callback function for session notifications
type IAudioSessionNotificationCallback struct {
	OnSessionCreated func(newSession *wca.IAudioSessionControl) error
}

// IAudioSessionNotification is a COM callback interface for new audio session notifications
type IAudioSessionNotification struct {
	vTable   *iAudioSessionNotificationVtbl
	refCount int
	callback IAudioSessionNotificationCallback
}

type iAudioSessionNotificationVtbl struct {
	ole.IUnknownVtbl
	OnSessionCreated uintptr
}

func asnQueryInterface(this uintptr, riid *ole.GUID, ppInterface *uintptr) int64 {
	*ppInterface = 0

	if ole.IsEqualGUID(riid, ole.IID_IUnknown) ||
		ole.IsEqualGUID(riid, wca.IID_IAudioSessionNotification) {
		asnAddRef(this)
		*ppInterface = this
		return ole.S_OK
	}

	return ole.E_NOINTERFACE
}

func asnAddRef(this uintptr) int64 {
	asn := (*IAudioSessionNotification)(unsafe.Pointer(this))
	asn.refCount++
	return int64(asn.refCount)
}

func asnRelease(this uintptr) int64 {
	asn := (*IAudioSessionNotification)(unsafe.Pointer(this))
	asn.refCount--
	return int64(asn.refCount)
}

func asnOnSessionCreated(this uintptr, newSession uintptr) int64 {
	asn := (*IAudioSessionNotification)(unsafe.Pointer(this))

	if asn.callback.OnSessionCreated == nil {
		return ole.S_OK
	}

	session := (*wca.IAudioSessionControl)(unsafe.Pointer(newSession))

	if err := asn.callback.OnSessionCreated(session); err != nil {
		return ole.E_FAIL
	}

	return ole.S_OK
}

// NewIAudioSessionNotification creates a new IAudioSessionNotification callback interface
func NewIAudioSessionNotification(callback IAudioSessionNotificationCallback) *IAudioSessionNotification {
	vTable := &iAudioSessionNotificationVtbl{}

	// IUnknown methods
	vTable.QueryInterface = syscall.NewCallback(asnQueryInterface)
	vTable.AddRef = syscall.NewCallback(asnAddRef)
	vTable.Release = syscall.NewCallback(asnRelease)

	// IAudioSessionNotification methods
	vTable.OnSessionCreated = syscall.NewCallback(asnOnSessionCreated)

	asn := &IAudioSessionNotification{}
	asn.vTable = vTable
	asn.callback = callback

	return asn
}

// ToWCA returns the pointer cast to wca.IAudioSessionNotification for use with WCA functions
func (asn *IAudioSessionNotification) ToWCA() *wca.IAudioSessionNotification {
	return (*wca.IAudioSessionNotification)(unsafe.Pointer(asn))
}
