//go:build windows

package tray

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")

	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procAttachConsole       = kernel32.NewProc("AttachConsole")
	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procGetMessageW         = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procPostMessageW        = user32.NewProc("PostMessageW")
	procLoadIconW           = user32.NewProc("LoadIconW")
	procCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	procAppendMenuW         = user32.NewProc("AppendMenuW")
	procDestroyMenu         = user32.NewProc("DestroyMenu")
	procTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procMessageBoxW         = user32.NewProc("MessageBoxW")
	procShell_NotifyIconW   = shell32.NewProc("Shell_NotifyIconW")
	procShellExecuteW       = shell32.NewProc("ShellExecuteW")
)

const (
	WM_APP          = 0x8000
	WM_TRAYICON     = WM_APP + 1
	WM_COMMAND      = 0x0111
	WM_RBUTTONUP    = 0x0205
	WM_LBUTTONUP    = 0x0202
	WM_CLOSE        = 0x0010
	WM_DESTROY      = 0x0002
	WM_NULL         = 0x0000

	NIM_ADD         = 0x00000000
	NIM_MODIFY      = 0x00000001
	NIM_DELETE      = 0x00000002

	NIF_MESSAGE     = 0x00000001
	NIF_ICON        = 0x00000002
	NIF_TIP         = 0x00000004
	NIF_INFO        = 0x00000010

	NIIF_INFO       = 0x00000001

	MF_STRING       = 0x00000000
	MF_SEPARATOR    = 0x00000800
	MF_GRAYED       = 0x00000001
	MF_DISABLED     = 0x00000002

	TPM_BOTTOMALIGN = 0x0020
	TPM_LEFTALIGN   = 0x0000
	TPM_RIGHTBUTTON = 0x0002

	IDI_APPLICATION = 32512

	MB_OK           = 0x00000000
	MB_YESNO        = 0x00000004
	MB_ICONINFO     = 0x00000040
	MB_ICONQUESTION = 0x00000020
	MB_ICONERROR    = 0x00000010
	IDYES           = 6
	IDNO            = 7
)

const (
	idHeader      = 2001
	idTallyStatus = 2002
	idOpenDash    = 2003
	idOpenConfig  = 2004
	idCheckUpdate = 2005
	idExit        = 2006
)

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

type point struct {
	x int32
	y int32
}

type msg struct {
	hWnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type notifyIconDataW struct {
	cbSize           uint32
	_                uint32
	hWnd             uintptr
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	_                uint32
	hIcon            uintptr
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         [16]byte
	hBalloonIcon     uintptr
}

var (
	currentAppCfg Config
	currentHwnd   uintptr
	currentNID    notifyIconDataW
	trayMu        sync.Mutex
)

// AttachParentConsole attaches to the parent console if started from Command Prompt or PowerShell.
func AttachParentConsole() {
	procAttachConsole.Call(^uintptr(0))
}

func stringToUTF16Array128(s string) [128]uint16 {
	var arr [128]uint16
	u, _ := syscall.UTF16FromString(s)
	copy(arr[:], u)
	return arr
}

func stringToUTF16Array256(s string) [256]uint16 {
	var arr [256]uint16
	u, _ := syscall.UTF16FromString(s)
	copy(arr[:], u)
	return arr
}

func stringToUTF16Array64(s string) [64]uint16 {
	var arr [64]uint16
	u, _ := syscall.UTF16FromString(s)
	copy(arr[:], u)
	return arr
}

func openURL(targetURL string) {
	urlPtr, _ := syscall.UTF16PtrFromString(targetURL)
	openPtr, _ := syscall.UTF16PtrFromString("open")
	procShellExecuteW.Call(0, uintptr(unsafe.Pointer(openPtr)), uintptr(unsafe.Pointer(urlPtr)), 0, 0, 1)
}

func openFile(path string) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}
	pathPtr, _ := syscall.UTF16PtrFromString(absPath)
	openPtr, _ := syscall.UTF16PtrFromString("open")
	procShellExecuteW.Call(0, uintptr(unsafe.Pointer(openPtr)), uintptr(unsafe.Pointer(pathPtr)), 0, 0, 1)
}

func showTrayBalloon(title, msg string) {
	trayMu.Lock()
	defer trayMu.Unlock()
	if currentHwnd == 0 {
		return
	}
	currentNID.uFlags = NIF_INFO
	currentNID.szInfoTitle = stringToUTF16Array64(title)
	currentNID.szInfo = stringToUTF16Array256(msg)
	currentNID.dwInfoFlags = NIIF_INFO
	procShell_NotifyIconW.Call(NIM_MODIFY, uintptr(unsafe.Pointer(&currentNID)))
}

func updateTrayTooltip(tip string) {
	trayMu.Lock()
	defer trayMu.Unlock()
	if currentHwnd == 0 {
		return
	}
	currentNID.uFlags = NIF_TIP
	currentNID.szTip = stringToUTF16Array128(tip)
	procShell_NotifyIconW.Call(NIM_MODIFY, uintptr(unsafe.Pointer(&currentNID)))
}

func restartApplication(hwnd uintptr, cfg Config) {
	exePath, err := os.Executable()
	if err != nil {
		return
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return
	}

	// Command to wait ~1.5s for current instance to exit cleanly and release TCP port 8080,
	// then start the new binary without opening a command window.
	// ping 127.0.0.1 -n 2 >nul provides a reliable ~1.5s delay across all Windows versions.
	cmdLine := fmt.Sprintf("/c ping 127.0.0.1 -n 2 >nul & start \"\" \"%s\"", exePath)
	cmdPtr, _ := syscall.UTF16PtrFromString("cmd.exe")
	argPtr, _ := syscall.UTF16PtrFromString(cmdLine)
	openPtr, _ := syscall.UTF16PtrFromString("open")
	dirPtr, _ := syscall.UTF16PtrFromString(filepath.Dir(exePath))

	// SW_HIDE = 0 so no cmd window is shown
	procShellExecuteW.Call(0, uintptr(unsafe.Pointer(openPtr)), uintptr(unsafe.Pointer(cmdPtr)), uintptr(unsafe.Pointer(argPtr)), uintptr(unsafe.Pointer(dirPtr)), 0)

	// Close tray window cleanly (which triggers cleanup and shutdown)
	procPostMessageW.Call(hwnd, WM_CLOSE, 0, 0)
}

func wndProc(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case WM_TRAYICON:
		if lParam == WM_RBUTTONUP || lParam == WM_LBUTTONUP {
			trayMu.Lock()
			cfg := currentAppCfg
			trayMu.Unlock()

			hMenu, _, _ := procCreatePopupMenu.Call()
			if hMenu == 0 {
				return 0
			}
			defer procDestroyMenu.Call(hMenu)

			headerText, _ := syscall.UTF16PtrFromString(fmt.Sprintf("🟢 Tally MCP Server (%s)", cfg.Version))
			procAppendMenuW.Call(hMenu, MF_STRING|MF_DISABLED|MF_GRAYED, idHeader, uintptr(unsafe.Pointer(headerText)))

			tallyText, _ := syscall.UTF16PtrFromString(fmt.Sprintf("Tally URL: %s", cfg.TallyURL))
			procAppendMenuW.Call(hMenu, MF_STRING|MF_DISABLED|MF_GRAYED, idTallyStatus, uintptr(unsafe.Pointer(tallyText)))

			procAppendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)

			dashText, _ := syscall.UTF16PtrFromString("🌐 Open Web Dashboard")
			procAppendMenuW.Call(hMenu, MF_STRING, idOpenDash, uintptr(unsafe.Pointer(dashText)))

			cfgText, _ := syscall.UTF16PtrFromString("📄 Open config.yaml")
			procAppendMenuW.Call(hMenu, MF_STRING, idOpenConfig, uintptr(unsafe.Pointer(cfgText)))

			updText, _ := syscall.UTF16PtrFromString("🔄 Check for Updates...")
			procAppendMenuW.Call(hMenu, MF_STRING, idCheckUpdate, uintptr(unsafe.Pointer(updText)))

			procAppendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)

			exitText, _ := syscall.UTF16PtrFromString("❌ Exit Tally MCP")
			procAppendMenuW.Call(hMenu, MF_STRING, idExit, uintptr(unsafe.Pointer(exitText)))

			var pt point
			procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
			procSetForegroundWindow.Call(hwnd)
			procTrackPopupMenu.Call(hMenu, TPM_BOTTOMALIGN|TPM_LEFTALIGN|TPM_RIGHTBUTTON, uintptr(pt.x), uintptr(pt.y), 0, hwnd, 0)
			procPostMessageW.Call(hwnd, WM_NULL, 0, 0)
			return 0
		}

	case WM_COMMAND:
		cmdID := int(wParam & 0xffff)
		trayMu.Lock()
		cfg := currentAppCfg
		trayMu.Unlock()

		switch cmdID {
		case idOpenDash:
			target := cfg.ListenURL
			if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
				target = "http://" + target
			}
			openURL(target)

		case idOpenConfig:
			if cfg.ConfigPath != "" {
				openFile(cfg.ConfigPath)
			} else {
				openFile("config.yaml")
			}

		case idCheckUpdate:
			go func() {
				if cfg.OnCheckUpdate != nil {
					ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
					defer cancel()

					hasUpdate, newVer, notes, err := cfg.OnCheckUpdate(ctx)
					titlePtr, _ := syscall.UTF16PtrFromString("Tally MCP Update Check")
					if err != nil {
						msgPtr, _ := syscall.UTF16PtrFromString(fmt.Sprintf("Failed to check for updates: %v", err))
						procMessageBoxW.Call(hwnd, uintptr(unsafe.Pointer(msgPtr)), uintptr(unsafe.Pointer(titlePtr)), MB_OK|MB_ICONERROR)
						return
					}

					if !hasUpdate {
						msgPtr, _ := syscall.UTF16PtrFromString(fmt.Sprintf("You are running the latest version (%s). No updates available.", cfg.Version))
						procMessageBoxW.Call(hwnd, uintptr(unsafe.Pointer(msgPtr)), uintptr(unsafe.Pointer(titlePtr)), MB_OK|MB_ICONINFO)
						return
					}

					// User prompt: ask whether they want to download and install the update
					var prompt string
					cleanNotes := strings.TrimSpace(notes)
					if cleanNotes != "" {
						if len(cleanNotes) > 300 {
							cleanNotes = cleanNotes[:300] + "..."
						}
						prompt = fmt.Sprintf("A new version (%s) is available!\n\nRelease Notes:\n%s\n\nDo you want to download and install the same?", newVer, cleanNotes)
					} else {
						prompt = fmt.Sprintf("A new version (%s) is available!\n\nDo you want to download and install the same?", newVer)
					}

					msgPtr, _ := syscall.UTF16PtrFromString(prompt)
					res, _, _ := procMessageBoxW.Call(hwnd, uintptr(unsafe.Pointer(msgPtr)), uintptr(unsafe.Pointer(titlePtr)), MB_YESNO|MB_ICONQUESTION)
					if res != IDYES {
						return
					}

					if cfg.OnApplyUpdate == nil {
						openURL(fmt.Sprintf("https://github.com/%s/releases", cfg.GitHubRepo))
						return
					}

					// Notify that download is starting
					showTrayBalloon("Tally MCP Update", fmt.Sprintf("Downloading and installing %s in background...", newVer))
					updateTrayTooltip(fmt.Sprintf("Tally MCP (Downloading %s...)", newVer))

					applyCtx, applyCancel := context.WithTimeout(context.Background(), 10*time.Minute)
					defer applyCancel()

					var lastProgress time.Time
					applyErr := cfg.OnApplyUpdate(applyCtx, func(downloaded, total int64) {
						if total > 0 && time.Since(lastProgress) > 1500*time.Millisecond {
							lastProgress = time.Now()
							pct := (downloaded * 100) / total
							updateTrayTooltip(fmt.Sprintf("Tally MCP (Downloading %s: %d%%)", newVer, pct))
						}
					})

					// Restore default tooltip
					updateTrayTooltip(fmt.Sprintf("Tally MCP Server (%s)", cfg.Version))

					if applyErr != nil {
						failMsg := fmt.Sprintf("Failed to download and install update: %v\n\nWould you like to open the GitHub Releases page to download it manually?", applyErr)
						failPtr, _ := syscall.UTF16PtrFromString(failMsg)
						resFail, _, _ := procMessageBoxW.Call(hwnd, uintptr(unsafe.Pointer(failPtr)), uintptr(unsafe.Pointer(titlePtr)), MB_YESNO|MB_ICONERROR)
						if resFail == IDYES {
							openURL(fmt.Sprintf("https://github.com/%s/releases", cfg.GitHubRepo))
						}
						return
					}

					// Successfully installed -> ask to restart
					successMsg := fmt.Sprintf("Update to %s has been downloaded and installed successfully!\n\nDo you want to restart Tally MCP now for changes to take effect?", newVer)
					successPtr, _ := syscall.UTF16PtrFromString(successMsg)
					resRestart, _, _ := procMessageBoxW.Call(hwnd, uintptr(unsafe.Pointer(successPtr)), uintptr(unsafe.Pointer(titlePtr)), MB_YESNO|MB_ICONINFO)
					if resRestart == IDYES {
						restartApplication(hwnd, cfg)
					} else {
						showTrayBalloon("Tally MCP Update", fmt.Sprintf("Update %s installed. It will take effect next time you start Tally MCP.", newVer))
					}
				}
			}()

		case idExit:
			procShell_NotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&currentNID)))
			procDestroyWindow.Call(hwnd)
			if cfg.OnQuit != nil {
				cfg.OnQuit()
			}
			return 0
		}

	case WM_CLOSE:
		procShell_NotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&currentNID)))
		procDestroyWindow.Call(hwnd)
		return 0

	case WM_DESTROY:
		procPostQuitMessage.Call(0)
		return 0
	}

	ret, _, _ := procDefWindowProcW.Call(hwnd, uintptr(message), wParam, lParam)
	return ret
}

// Run creates the hidden window and notification area tray icon and runs the message loop.
func Run(ctx context.Context, cfg Config) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	trayMu.Lock()
	currentAppCfg = cfg
	trayMu.Unlock()

	hInstance, _, _ := procGetModuleHandleW.Call(0)
	className, _ := syscall.UTF16PtrFromString("TallyMCPServerTrayClass")
	windowTitle, _ := syscall.UTF16PtrFromString("Tally MCP Server")

	hIcon, _, _ := procLoadIconW.Call(0, IDI_APPLICATION)

	var wc wndClassExW
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	wc.lpfnWndProc = syscall.NewCallback(wndProc)
	wc.hInstance = hInstance
	wc.hIcon = hIcon
	wc.lpszClassName = className

	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	hwnd, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(windowTitle)),
		0,
		0, 0, 0, 0,
		0,
		0,
		hInstance,
		0,
	)

	if hwnd == 0 {
		return fmt.Errorf("failed to create tray window")
	}
	currentHwnd = hwnd

	// Add icon to system tray
	var nid notifyIconDataW
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.hWnd = hwnd
	nid.uID = 1
	nid.uFlags = NIF_MESSAGE | NIF_ICON | NIF_TIP | NIF_INFO
	nid.uCallbackMessage = WM_TRAYICON
	nid.hIcon = hIcon
	nid.szTip = stringToUTF16Array128(fmt.Sprintf("Tally MCP Server (%s)", cfg.Version))
	nid.szInfoTitle = stringToUTF16Array64("Tally MCP Server")
	nid.szInfo = stringToUTF16Array256(fmt.Sprintf("Server is running in system tray.\nDashboard: %s", cfg.ListenURL))
	nid.dwInfoFlags = NIIF_INFO

	currentNID = nid
	procShell_NotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&nid)))

	// Close tray window if context is canceled externally
	go func() {
		<-ctx.Done()
		if currentHwnd != 0 {
			procPostMessageW.Call(currentHwnd, WM_CLOSE, 0, 0)
		}
	}()

	// Windows Message Loop
	var m msg
	for {
		ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}

	procShell_NotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&nid)))

	if cfg.OnQuit != nil {
		cfg.OnQuit()
	}

	return nil
}
