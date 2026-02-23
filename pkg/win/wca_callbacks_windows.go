package win

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

// Shared vtables — syscall.NewCallback has a process-lifetime limit of 2048 calls,
// so each vtable is allocated once and reused across all instances of that type.
var (
	aseVTable  iAudioSessionEventsVtbl
	asnVTable  iAudioSessionNotificationVtbl
	mmncVTable iMMNotificationClientVtbl
)

func init() {
	aseVTable.QueryInterface = syscall.NewCallback(aseQueryInterface)
	aseVTable.AddRef = syscall.NewCallback(aseAddRef)
	aseVTable.Release = syscall.NewCallback(aseRelease)
	aseVTable.OnDisplayNameChanged = syscall.NewCallback(aseOnDisplayNameChanged)
	aseVTable.OnIconPathChanged = syscall.NewCallback(aseOnIconPathChanged)
	aseVTable.OnSimpleVolumeChanged = syscall.NewCallback(aseOnSimpleVolumeChanged)
	aseVTable.OnChannelVolumeChanged = syscall.NewCallback(aseOnChannelVolumeChanged)
	aseVTable.OnGroupingParamChanged = syscall.NewCallback(aseOnGroupingParamChanged)
	aseVTable.OnStateChanged = syscall.NewCallback(aseOnStateChanged)
	aseVTable.OnSessionDisconnected = syscall.NewCallback(aseOnSessionDisconnected)

	asnVTable.QueryInterface = syscall.NewCallback(asnQueryInterface)
	asnVTable.AddRef = syscall.NewCallback(asnAddRef)
	asnVTable.Release = syscall.NewCallback(asnRelease)
	asnVTable.OnSessionCreated = syscall.NewCallback(asnOnSessionCreated)

	mmncVTable.QueryInterface = syscall.NewCallback(mmncQueryInterface)
	mmncVTable.AddRef = syscall.NewCallback(mmncAddRef)
	mmncVTable.Release = syscall.NewCallback(mmncRelease)
	mmncVTable.OnDeviceStateChanged = syscall.NewCallback(mmncOnDeviceStateChanged)
	mmncVTable.OnDeviceAdded = syscall.NewCallback(mmncOnDeviceAdded)
	mmncVTable.OnDeviceRemoved = syscall.NewCallback(mmncOnDeviceRemoved)
	mmncVTable.OnDefaultDeviceChanged = syscall.NewCallback(mmncOnDefaultDeviceChanged)
	mmncVTable.OnPropertyValueChanged = syscall.NewCallback(mmncOnPropertyValueChanged)
}

// NewIAudioSessionEvents creates a new IAudioSessionEvents callback interface
func NewIAudioSessionEvents(callback IAudioSessionEventsCallback) *IAudioSessionEvents {
	ase := &IAudioSessionEvents{}
	ase.vTable = &aseVTable
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
	asn := &IAudioSessionNotification{}
	asn.vTable = &asnVTable
	asn.callback = callback

	return asn
}

// ToWCA returns the pointer cast to wca.IAudioSessionNotification for use with WCA functions
func (asn *IAudioSessionNotification) ToWCA() *wca.IAudioSessionNotification {
	return (*wca.IAudioSessionNotification)(unsafe.Pointer(asn))
}

// IMMNotificationClientCallback contains the callback functions for device notifications
// This is a fixed version of go-wca's implementation that correctly passes dwNewState
type IMMNotificationClientCallback struct {
	OnDeviceStateChanged   func(pwstrDeviceId string, dwNewState uint32) error
	OnDeviceAdded          func(pwstrDeviceId string) error
	OnDeviceRemoved        func(pwstrDeviceId string) error
	OnDefaultDeviceChanged func(flow wca.EDataFlow, role wca.ERole, pwstrDefaultDeviceId string) error
	OnPropertyValueChanged func(pwstrDeviceId string, key uint64) error
}

// IMMNotificationClient is a COM callback interface for device notifications
type IMMNotificationClient struct {
	vTable   *iMMNotificationClientVtbl
	refCount int
	callback IMMNotificationClientCallback
}

type iMMNotificationClientVtbl struct {
	ole.IUnknownVtbl
	OnDeviceStateChanged   uintptr
	OnDeviceAdded          uintptr
	OnDeviceRemoved        uintptr
	OnDefaultDeviceChanged uintptr
	OnPropertyValueChanged uintptr
}

func mmncQueryInterface(this uintptr, riid *ole.GUID, ppInterface *uintptr) int64 {
	*ppInterface = 0

	if ole.IsEqualGUID(riid, ole.IID_IUnknown) ||
		ole.IsEqualGUID(riid, wca.IID_IMMNotificationClient) {
		mmncAddRef(this)
		*ppInterface = this
		return ole.S_OK
	}

	return ole.E_NOINTERFACE
}

func mmncAddRef(this uintptr) int64 {
	mmnc := (*IMMNotificationClient)(unsafe.Pointer(this))
	mmnc.refCount++
	return int64(mmnc.refCount)
}

func mmncRelease(this uintptr) int64 {
	mmnc := (*IMMNotificationClient)(unsafe.Pointer(this))
	mmnc.refCount--
	return int64(mmnc.refCount)
}

func mmncOnDeviceStateChanged(this uintptr, pwstrDeviceId uintptr, dwNewState uintptr) int64 {
	mmnc := (*IMMNotificationClient)(unsafe.Pointer(this))

	if mmnc.callback.OnDeviceStateChanged == nil {
		return ole.S_OK
	}

	device := wca.LPCWSTRToString(pwstrDeviceId, 1024)

	// Fixed: pass actual dwNewState instead of hardcoded 0
	if err := mmnc.callback.OnDeviceStateChanged(device, uint32(dwNewState)); err != nil {
		return ole.E_FAIL
	}

	return ole.S_OK
}

func mmncOnDeviceAdded(this uintptr, pwstrDeviceId uintptr) int64 {
	mmnc := (*IMMNotificationClient)(unsafe.Pointer(this))

	if mmnc.callback.OnDeviceAdded == nil {
		return ole.S_OK
	}

	device := wca.LPCWSTRToString(pwstrDeviceId, 1024)

	if err := mmnc.callback.OnDeviceAdded(device); err != nil {
		return ole.E_FAIL
	}

	return ole.S_OK
}

func mmncOnDeviceRemoved(this uintptr, pwstrDeviceId uintptr) int64 {
	mmnc := (*IMMNotificationClient)(unsafe.Pointer(this))

	if mmnc.callback.OnDeviceRemoved == nil {
		return ole.S_OK
	}

	device := wca.LPCWSTRToString(pwstrDeviceId, 1024)

	if err := mmnc.callback.OnDeviceRemoved(device); err != nil {
		return ole.E_FAIL
	}

	return ole.S_OK
}

func mmncOnDefaultDeviceChanged(this uintptr, flow, role uint64, pwstrDeviceId uintptr) int64 {
	mmnc := (*IMMNotificationClient)(unsafe.Pointer(this))

	if mmnc.callback.OnDefaultDeviceChanged == nil {
		return ole.S_OK
	}

	device := wca.LPCWSTRToString(pwstrDeviceId, 1024)

	if err := mmnc.callback.OnDefaultDeviceChanged(wca.EDataFlow(flow), wca.ERole(role), device); err != nil {
		return ole.E_FAIL
	}

	return ole.S_OK
}

func mmncOnPropertyValueChanged(this uintptr, pwstrDeviceId uintptr, key uintptr) int64 {
	mmnc := (*IMMNotificationClient)(unsafe.Pointer(this))

	if mmnc.callback.OnPropertyValueChanged == nil {
		return ole.S_OK
	}

	device := wca.LPCWSTRToString(pwstrDeviceId, 1024)

	// Fixed: pass actual key instead of hardcoded 0
	if err := mmnc.callback.OnPropertyValueChanged(device, uint64(key)); err != nil {
		return ole.E_FAIL
	}

	return ole.S_OK
}

// NewIMMNotificationClient creates a new IMMNotificationClient callback interface
// This is a fixed version that correctly passes dwNewState in OnDeviceStateChanged
func NewIMMNotificationClient(callback IMMNotificationClientCallback) *IMMNotificationClient {
	mmnc := &IMMNotificationClient{}
	mmnc.vTable = &mmncVTable
	mmnc.callback = callback

	return mmnc
}

// ToWCA returns the pointer cast to wca.IMMNotificationClient for use with WCA functions
func (mmnc *IMMNotificationClient) ToWCA() *wca.IMMNotificationClient {
	return (*wca.IMMNotificationClient)(unsafe.Pointer(mmnc))
}

// GetDevice calls IMMDeviceEnumerator::GetDevice directly via vtable,
// working around go-wca's unimplemented stub that always returns E_NOTIMPL.
func GetDevice(mmde *wca.IMMDeviceEnumerator, pwstrId string, ppDevice **wca.IMMDevice) error {
	idPtr, err := syscall.UTF16PtrFromString(pwstrId)
	if err != nil {
		return err
	}

	hr, _, _ := syscall.SyscallN(
		mmde.VTable().GetDevice,
		uintptr(unsafe.Pointer(mmde)),
		uintptr(unsafe.Pointer(idPtr)),
		uintptr(unsafe.Pointer(ppDevice)),
	)

	if hr != 0 {
		return ole.NewError(hr)
	}

	return nil
}
