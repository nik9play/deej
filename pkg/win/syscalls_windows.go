package win

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modshell32 = windows.NewLazySystemDLL("shell32.dll")
	moduser32  = windows.NewLazySystemDLL("user32.dll")

	procSHQueryUserNotificationState = modshell32.NewProc("SHQueryUserNotificationState")
	procGetWindowRect                = moduser32.NewProc("GetWindowRect")
	procMonitorFromRect              = moduser32.NewProc("MonitorFromRect")
	procGetMonitorInfo               = moduser32.NewProc("GetMonitorInfoW")
	procIntersectRect                = moduser32.NewProc("IntersectRect")
	procEqualRect                    = moduser32.NewProc("EqualRect")
	procGetWindowLong                = moduser32.NewProc("GetWindowLongPtrW")
)

const (
	QUNS_NOT_PRESENT             = 1
	QUNS_BUSY                    = 2
	QUNS_RUNNING_D3D_FULL_SCREEN = 3
	QUNS_PRESENTATION_MODE       = 4
	QUNS_ACCEPTS_NOTIFICATIONS   = 5
	QUNS_QUIET_TIME              = 6
	QUNS_APP                     = 7
)

const (
	GWL_EXSTYLE     = -20
	GWL_STYLE       = -16
	GWL_WNDPROC     = -4
	GWLP_WNDPROC    = -4
	GWL_HINSTANCE   = -6
	GWLP_HINSTANCE  = -6
	GWL_HWNDPARENT  = -8
	GWLP_HWNDPARENT = -8
	GWL_ID          = -12
	GWLP_ID         = -12
	GWL_USERDATA    = -21
	GWLP_USERDATA   = -21
)

// Window style constants
const (
	WS_OVERLAPPED       = 0x00000000
	WS_POPUP            = 0x80000000
	WS_CHILD            = 0x40000000
	WS_MINIMIZE         = 0x20000000
	WS_VISIBLE          = 0x10000000
	WS_DISABLED         = 0x08000000
	WS_CLIPSIBLINGS     = 0x04000000
	WS_CLIPCHILDREN     = 0x02000000
	WS_MAXIMIZE         = 0x01000000
	WS_CAPTION          = 0x00C00000
	WS_BORDER           = 0x00800000
	WS_DLGFRAME         = 0x00400000
	WS_VSCROLL          = 0x00200000
	WS_HSCROLL          = 0x00100000
	WS_SYSMENU          = 0x00080000
	WS_THICKFRAME       = 0x00040000
	WS_GROUP            = 0x00020000
	WS_TABSTOP          = 0x00010000
	WS_MINIMIZEBOX      = 0x00020000
	WS_MAXIMIZEBOX      = 0x00010000
	WS_TILED            = 0x00000000
	WS_ICONIC           = 0x20000000
	WS_SIZEBOX          = 0x00040000
	WS_OVERLAPPEDWINDOW = 0x00000000 | 0x00C00000 | 0x00080000 | 0x00040000 | 0x00020000 | 0x00010000
	WS_POPUPWINDOW      = 0x80000000 | 0x00800000 | 0x00080000
	WS_CHILDWINDOW      = 0x40000000
)

// Extended window style constants
const (
	WS_EX_DLGMODALFRAME    = 0x00000001
	WS_EX_NOPARENTNOTIFY   = 0x00000004
	WS_EX_TOPMOST          = 0x00000008
	WS_EX_ACCEPTFILES      = 0x00000010
	WS_EX_TRANSPARENT      = 0x00000020
	WS_EX_MDICHILD         = 0x00000040
	WS_EX_TOOLWINDOW       = 0x00000080
	WS_EX_WINDOWEDGE       = 0x00000100
	WS_EX_CLIENTEDGE       = 0x00000200
	WS_EX_CONTEXTHELP      = 0x00000400
	WS_EX_RIGHT            = 0x00001000
	WS_EX_LEFT             = 0x00000000
	WS_EX_RTLREADING       = 0x00002000
	WS_EX_LTRREADING       = 0x00000000
	WS_EX_LEFTSCROLLBAR    = 0x00004000
	WS_EX_RIGHTSCROLLBAR   = 0x00000000
	WS_EX_CONTROLPARENT    = 0x00010000
	WS_EX_STATICEDGE       = 0x00020000
	WS_EX_APPWINDOW        = 0x00040000
	WS_EX_OVERLAPPEDWINDOW = 0x00000100 | 0x00000200
	WS_EX_PALETTEWINDOW    = 0x00000100 | 0x00000080 | 0x00000008
	WS_EX_LAYERED          = 0x00080000
	WS_EX_NOINHERITLAYOUT  = 0x00100000
	WS_EX_LAYOUTRTL        = 0x00400000
	WS_EX_COMPOSITED       = 0x02000000
	WS_EX_NOACTIVATE       = 0x08000000
)

const (
	MONITOR_DEFAULTTONULL    = 0x0
	MONITOR_DEFAULTTOPRIMARY = 0x1
	MONITOR_DEFAULTTONEAREST = 0x2
)

type RECT struct {
	Left,
	Top,
	Right,
	Bottom int32
}

type MONITORINFO struct {
	CbSize uint32
	Monitor,
	WorkArea RECT
	Flags uint32
}

func retErr(r1, _ uintptr, lastErr error) (err error) {
	if r1 == 0 {
		err = lastErr
	}

	return
}

func SHQueryUserNotificationState(state *uint32) error {
	return retErr(procSHQueryUserNotificationState.Call(uintptr(unsafe.Pointer(state))))
}

func GetWindowRect(hwnd windows.HWND, rect *RECT) error {
	return retErr(procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(rect))))
}

func MonitorFromRect(rect *RECT, flags uint32) (handle windows.Handle, err error) {
	r0, _, e1 := procMonitorFromRect.Call(uintptr(unsafe.Pointer(rect)), uintptr(flags))
	handle = windows.Handle(r0)

	if r0 == 0 {
		err = e1
	}

	return
}

func GetMonitorInfo(handle windows.Handle, mi *MONITORINFO) error {
	return retErr(procGetMonitorInfo.Call(uintptr(handle), uintptr(unsafe.Pointer(mi))))
}

func IntersectRect(rectOut *RECT, rect1 *RECT, rect2 *RECT) bool {
	r0, _, _ := procIntersectRect.Call(uintptr(unsafe.Pointer(rectOut)), uintptr(unsafe.Pointer(rect1)), uintptr(unsafe.Pointer(rect2)))

	return r0 != 0
}

func EqualRect(rect1 *RECT, rect2 *RECT) bool {
	r0, _, _ := procEqualRect.Call(uintptr(unsafe.Pointer(rect1)), uintptr(unsafe.Pointer(rect2)))

	return r0 != 0
}

func GetWindowLong(hwnd windows.HWND, nindex int32) (style uintptr, err error) {
	r0, _, e1 := procGetWindowLong.Call(uintptr(hwnd), uintptr(nindex))
	style = r0

	if r0 == 0 {
		err = e1
	}

	return
}
