package util

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"github.com/mitchellh/go-ps"
	"github.com/nik9play/deej/pkg/win"
	"golang.org/x/sys/windows"
)

const (
	getCurrentWindowInternalCooldown = time.Millisecond * 350
)

var (
	lastGetCurrentWindowResult []string
	lastGetCurrentWindowCall   = time.Now()
)

func getCurrentWindowProcessNames(checkFullscreen bool) ([]string, error) {

	// apply an internal cooldown on this function to avoid calling windows API functions too frequently.
	// return a cached value during that cooldown
	now := time.Now()
	if lastGetCurrentWindowCall.Add(getCurrentWindowInternalCooldown).After(now) {
		return lastGetCurrentWindowResult, nil
	}

	lastGetCurrentWindowCall = now

	// the logic of this implementation is a bit convoluted because of the way UWP apps
	// (also known as "modern win 10 apps" or "microsoft store apps") work.
	// these are rendered in a parent container by the name of ApplicationFrameHost.exe.
	// when windows's GetForegroundWindow is called, it returns the window owned by that parent process.
	// so whenever we get that, we need to go and look through its child windows until we find one with a different PID.
	// this behavior is most common with UWP, but it actually applies to any "container" process:
	// an acceptable approach is to return a slice of possible process names that could be the "right" one, looking
	// them up is fairly cheap and covers the most bases for apps that hide their audio-playing inside another process
	// (like steam, and the league client, and any UWP app)

	result := []string{}

	// a callback that will be called for each child window of the foreground window, if it has any
	enumChildWindowsCallback := func(childHWND *uintptr, lParam *uintptr) uintptr {

		// cast the outer lp into something we can work with (maybe closures are good enough?)
		ownerPID := (*uint32)(unsafe.Pointer(lParam))

		// get the child window's real PID
		var childPID uint32
		windows.GetWindowThreadProcessId((windows.HWND)(unsafe.Pointer(childHWND)), &childPID)

		// compare it to the parent's - if they're different, add the child window's process to our list of process names
		if childPID != *ownerPID {

			// warning: this can silently fail, needs to be tested more thoroughly and possibly reverted in the future
			actualProcess, err := ps.FindProcess(int(childPID))
			if err == nil {
				result = append(result, actualProcess.Executable())
			}
		}

		// indicates to the system to keep iterating
		return 1
	}

	// get the current foreground window
	hwnd := windows.GetForegroundWindow()

	if checkFullscreen {
		if !isWindowFullscreen(hwnd) {
			return nil, nil
		}
	}

	var ownerPID uint32

	// get its PID and put it in our window info struct
	windows.GetWindowThreadProcessId(hwnd, &ownerPID)

	// check for system PID (0)
	if ownerPID == 0 {
		return nil, nil
	}

	// find the process name corresponding to the parent PID
	process, err := ps.FindProcess(int(ownerPID))
	if err != nil {
		return nil, fmt.Errorf("get parent process for pid %d: %w", ownerPID, err)
	}

	// add it to our result slice
	result = append(result, process.Executable())

	// iterate its child windows, adding their names too
	windows.EnumChildWindows(hwnd, syscall.NewCallback(enumChildWindowsCallback), (unsafe.Pointer(&ownerPID)))

	// cache & return whichever executable names we ended up with
	lastGetCurrentWindowResult = result
	return result, nil
}

func getOpenExternalCommand(filename string) *exec.Cmd {
	return exec.Command(filepath.Join(os.Getenv("SYSTEMROOT"), "System32", "rundll32.exe"), "url.dll,FileProtocolHandler", filename)
}

// check if the window is in fullscreen mode
//
// inspired by https://chromium.googlesource.com/chromium/src/+/refs/tags/134.0.6996.1/ui/base/fullscreen_win.cc
func isWindowFullscreen(hwnd windows.HWND) bool {
	var state uint32

	// this function checks for fullscreen apps in exclusive mode
	err := win.SHQueryUserNotificationState(&state)
	if err != nil {
		return false
	}

	if state == win.QUNS_RUNNING_D3D_FULL_SCREEN || state == win.QUNS_PRESENTATION_MODE {
		return true
	}

	// get the monitor where the window is located
	var wndRect win.RECT
	err = win.GetWindowRect(hwnd, &wndRect)
	if err != nil {
		return false
	}

	monitor, err := win.MonitorFromRect(&wndRect, win.MONITOR_DEFAULTTONULL)
	if err != nil {
		return false
	}

	var monitorInfo win.MONITORINFO
	monitorInfo.CbSize = uint32(unsafe.Sizeof(monitorInfo))
	err = win.GetMonitorInfo(monitor, &monitorInfo)
	if err != nil {
		return false
	}

	// the window should be at least as large as the monitor
	if !win.IntersectRect(&wndRect, &wndRect, &monitorInfo.Monitor) || !win.EqualRect(&wndRect, &monitorInfo.Monitor) {
		return false
	}

	style, err := win.GetWindowLong(hwnd, win.GWL_STYLE)
	if err != nil {
		return false
	}

	exStyle, err := win.GetWindowLong(hwnd, win.GWL_EXSTYLE)
	if err != nil {
		return false
	}

	// I doubt that this check is necessary
	return !((style&(win.WS_DLGFRAME|win.WS_THICKFRAME)) != 0 ||
		(exStyle&(win.WS_EX_WINDOWEDGE|win.WS_EX_TOOLWINDOW)) != 0)
}
