// SPDX-License-Identifier: MIT

package screenlock

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	wtsapi32 = windows.NewLazySystemDLL("wtsapi32.dll")

	procRegisterClassExW  = user32.NewProc("RegisterClassExW")
	procCreateWindowExW   = user32.NewProc("CreateWindowExW")
	procDefWindowProcW    = user32.NewProc("DefWindowProcW")
	procGetMessageW       = user32.NewProc("GetMessageW")
	procDispatchMessageW  = user32.NewProc("DispatchMessageW")
	procPostMessageW      = user32.NewProc("PostMessageW")
	procDestroyWindow     = user32.NewProc("DestroyWindow")
	procWTSRegisterNotify = wtsapi32.NewProc("WTSRegisterSessionNotification")
	procWTSUnRegister     = wtsapi32.NewProc("WTSUnRegisterSessionNotification")
)

const (
	wmClose              = 0x0010
	wmWTSSessionChange   = 0x02B1
	wtsSessionLock       = 0x7
	notifyForThisSession = 0
	hwndMessage          = ^uintptr(2) // (HWND)-3
)

type wndClassEx struct {
	size, style                        uint32
	wndProc                            uintptr
	clsExtra, wndExtra                 int32
	instance, icon, cursor, background windows.Handle
	menuName, className                *uint16
	iconSm                             windows.Handle
}

type msg struct {
	hwnd    windows.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
}

// The window procedure is shared (one callback per process); it reports to
// the watcher of its window.
var (
	wmu             sync.Mutex
	onLockOf        = map[windows.Handle]func(){}
	classRegistered bool
)

var wndProc = syscall.NewCallback(func(hwnd windows.Handle, m uint32, wp, lp uintptr) uintptr {
	if m == wmWTSSessionChange && wp == wtsSessionLock {
		wmu.Lock()
		f := onLockOf[hwnd]
		wmu.Unlock()
		if f != nil {
			go f()
		}
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(m), wp, lp)
	return r
})

// watch creates a message-only window on a locked thread, registers it for
// the session notifications and pumps its messages until ctx ends.
func watch(ctx context.Context, onLock func()) error {
	errc := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		name, _ := windows.UTF16PtrFromString("VaultcrabScreenLock")
		wmu.Lock()
		if !classRegistered {
			wc := wndClassEx{wndProc: wndProc, className: name}
			wc.size = uint32(unsafe.Sizeof(wc))
			if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
				wmu.Unlock()
				errc <- fmt.Errorf("%w: RegisterClassEx: %v", ErrUnsupported, err)
				return
			}
			classRegistered = true
		}
		wmu.Unlock()
		h, _, err := procCreateWindowExW.Call(0, uintptr(unsafe.Pointer(name)), 0, 0, 0, 0, 0, 0, hwndMessage, 0, 0, 0)
		if h == 0 {
			errc <- fmt.Errorf("%w: CreateWindowEx: %v", ErrUnsupported, err)
			return
		}
		hwnd := windows.Handle(h)
		if r, _, err := procWTSRegisterNotify.Call(h, notifyForThisSession); r == 0 {
			procDestroyWindow.Call(h)
			errc <- fmt.Errorf("%w: WTSRegisterSessionNotification: %v", ErrUnsupported, err)
			return
		}
		wmu.Lock()
		onLockOf[hwnd] = onLock
		wmu.Unlock()
		errc <- nil
		go func() {
			<-ctx.Done()
			procPostMessageW.Call(h, wmClose, 0, 0)
		}()
		var m msg
		for {
			r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if int32(r) <= 0 {
				break
			}
			if m.message == wmClose {
				break
			}
			procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
		procWTSUnRegister.Call(h)
		procDestroyWindow.Call(h)
		wmu.Lock()
		delete(onLockOf, hwnd)
		wmu.Unlock()
	}()
	return <-errc
}
