// SPDX-FileCopyrightText: 2026 startclean contributors <https://github.com/P4suta/startclean/graphs/contributors>
// SPDX-License-Identifier: MIT OR Apache-2.0

//go:build windows

package platform

import (
	"errors"
	"fmt"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"github.com/P4suta/startclean/internal/core"
	"golang.org/x/sys/windows"
)

const (
	clsctxInprocServer   = 0x1
	coinitApartment      = 0x2
	slpgRawPath          = 0x4
	driveRemovable       = 2
	driveFixed           = 3
	driveRemote          = 4
	shcneDelete          = 0x00000004
	shcneRmdir           = 0x00000010
	shcnfPathW           = 0x0005
	rpcEChangedMode      = 0x80010106
	maxShortcutPathUTF16 = 32768
)

var (
	shell32                  = windows.NewLazySystemDLL("shell32.dll")
	ole32                    = windows.NewLazySystemDLL("ole32.dll")
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procSHGetKnownFolderPath = shell32.NewProc("SHGetKnownFolderPath")
	procSHChangeNotify       = shell32.NewProc("SHChangeNotify")
	procCoTaskMemFree        = ole32.NewProc("CoTaskMemFree")
	procCoInitializeEx       = ole32.NewProc("CoInitializeEx")
	procCoUninitialize       = ole32.NewProc("CoUninitialize")
	procCoCreateInstance     = ole32.NewProc("CoCreateInstance")
	procGetDriveTypeW        = kernel32.NewProc("GetDriveTypeW")
	procExpandEnvW           = kernel32.NewProc("ExpandEnvironmentStringsW")

	folderIDPrograms = windows.GUID{
		Data1: 0xA77F5D77, Data2: 0x2E2B, Data3: 0x44C3,
		Data4: [8]byte{0xA6, 0xA2, 0xAB, 0xA6, 0x01, 0x05, 0x4A, 0x51},
	}
	folderIDCommonPrograms = windows.GUID{
		Data1: 0x0139D44E, Data2: 0x6AFE, Data3: 0x49F2,
		Data4: [8]byte{0x86, 0x90, 0x3D, 0xAF, 0xCA, 0xE6, 0xFF, 0xB8},
	}
	clsidShellLink = windows.GUID{
		Data1: 0x00021401, Data2: 0x0000, Data3: 0x0000,
		Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46},
	}
	iidShellLinkW = windows.GUID{
		Data1: 0x000214F9, Data2: 0x0000, Data3: 0x0000,
		Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46},
	}
	iidPersistFile = windows.GUID{
		Data1: 0x0000010B, Data2: 0x0000, Data3: 0x0000,
		Data4: [8]byte{0xC0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46},
	}
)

type System struct{}

func New() *System { return &System{} }

func (s *System) Supported() bool { return true }

func (s *System) Roots() (core.Roots, error) {
	user, userErr := knownFolder(folderIDPrograms)
	common, commonErr := knownFolder(folderIDCommonPrograms)
	if userErr != nil || commonErr != nil {
		return core.Roots{User: user, Common: common}, fmt.Errorf(
			"discover Start Menu known folders: %w", errors.Join(userErr, commonErr),
		)
	}
	return core.Roots{User: user, Common: common}, nil
}

func knownFolder(id windows.GUID) (string, error) {
	var raw *uint16
	result, _, _ := procSHGetKnownFolderPath.Call(
		uintptr(unsafe.Pointer(&id)), 0, 0, uintptr(unsafe.Pointer(&raw)), //nolint:gosec // Audited Windows ABI pointers.
	)
	if failedHRESULT(result) {
		return "", hresultError("SHGetKnownFolderPath", result)
	}
	if raw == nil {
		return "", fmt.Errorf("SHGetKnownFolderPath returned a nil path")
	}
	defer func() { _, _, _ = procCoTaskMemFree.Call(uintptr(unsafe.Pointer(raw))) }() //nolint:gosec // Paired Windows ABI allocation.
	return windows.UTF16PtrToString(raw), nil
}

func (s *System) Elevated() bool {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer func() { _ = token.Close() }()
	return token.IsElevated()
}

func (s *System) Target(linkPath string) (string, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	initResult, _, _ := procCoInitializeEx.Call(0, coinitApartment)
	switch uint32(initResult) { //nolint:gosec // HRESULT is defined as a signed 32-bit value.
	case 0, 1:
		defer func() { _, _, _ = procCoUninitialize.Call() }()
	case rpcEChangedMode:
		// COM is already initialized using another apartment model. The current
		// thread can still use the in-process Shell Link object.
	default:
		if failedHRESULT(initResult) {
			return "", hresultError("CoInitializeEx", initResult)
		}
	}

	var shellLink unsafe.Pointer
	result, _, _ := procCoCreateInstance.Call(
		uintptr(unsafe.Pointer(&clsidShellLink)), //nolint:gosec // Audited COM ABI pointer.
		0,
		clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidShellLinkW)), //nolint:gosec // Audited COM ABI pointer.
		uintptr(unsafe.Pointer(&shellLink)),     //nolint:gosec // Audited COM out-parameter.
	)
	if failedHRESULT(result) {
		return "", hresultError("CoCreateInstance(CLSID_ShellLink)", result)
	}
	if shellLink == nil {
		return "", fmt.Errorf("CoCreateInstance returned a nil IShellLinkW")
	}
	defer comRelease(shellLink)

	var persistFile unsafe.Pointer
	result = comCall(shellLink, 0, uintptr(unsafe.Pointer(&iidPersistFile)), uintptr(unsafe.Pointer(&persistFile))) //nolint:gosec // Audited COM ABI pointers.
	if failedHRESULT(result) {
		return "", hresultError("IShellLinkW.QueryInterface(IPersistFile)", result)
	}
	if persistFile == nil {
		return "", fmt.Errorf("QueryInterface returned a nil IPersistFile")
	}
	defer comRelease(persistFile)

	linkPathUTF16, err := windows.UTF16PtrFromString(linkPath)
	if err != nil {
		return "", fmt.Errorf("encode shortcut path: %w", err)
	}
	result = comCall(persistFile, 5, uintptr(unsafe.Pointer(linkPathUTF16)), 0) //nolint:gosec // Audited UTF-16 COM input.
	runtime.KeepAlive(linkPathUTF16)
	if failedHRESULT(result) {
		return "", hresultError("IPersistFile.Load", result)
	}

	targetBuffer := make([]uint16, maxShortcutPathUTF16)
	result = comCall(
		shellLink,
		3,
		uintptr(unsafe.Pointer(&targetBuffer[0])), //nolint:gosec // Audited COM output buffer.
		uintptr(len(targetBuffer)),
		0,
		slpgRawPath,
	)
	if failedHRESULT(result) {
		return "", hresultError("IShellLinkW.GetPath", result)
	}
	return windows.UTF16ToString(targetBuffer), nil
}

func (s *System) ExpandEnvironment(value string) (string, error) {
	source, err := windows.UTF16PtrFromString(value)
	if err != nil {
		return "", fmt.Errorf("encode environment string: %w", err)
	}
	required, _, callErr := procExpandEnvW.Call(uintptr(unsafe.Pointer(source)), 0, 0) //nolint:gosec // Audited UTF-16 Win32 input.
	if required == 0 {
		return "", fmt.Errorf("size expanded environment string: %w", callErr)
	}
	buffer := make([]uint16, required)
	written, _, callErr := procExpandEnvW.Call(
		uintptr(unsafe.Pointer(source)),     //nolint:gosec // Audited UTF-16 Win32 input.
		uintptr(unsafe.Pointer(&buffer[0])), //nolint:gosec // Audited sized Win32 output buffer.
		uintptr(len(buffer)),
	)
	if written == 0 || written > uintptr(len(buffer)) {
		return "", fmt.Errorf("expand environment string: %w", callErr)
	}
	return windows.UTF16ToString(buffer), nil
}

func (s *System) DriveKind(root string) (core.DriveKind, error) {
	rootUTF16, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return core.DriveOther, fmt.Errorf("encode drive root: %w", err)
	}
	kind, _, _ := procGetDriveTypeW.Call(uintptr(unsafe.Pointer(rootUTF16))) //nolint:gosec // Audited UTF-16 Win32 input.
	switch kind {
	case driveFixed:
		return core.DriveFixed, nil
	case driveRemovable:
		return core.DriveRemovable, nil
	case driveRemote:
		return core.DriveNetwork, nil
	default:
		return core.DriveOther, nil
	}
}

func (s *System) Deleted(path string) {
	notifyShell(shcneDelete, path)
}

func (s *System) DirectoryRemoved(path string) {
	notifyShell(shcneRmdir, path)
}

func notifyShell(event uintptr, path string) {
	encoded, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return
	}
	_, _, _ = procSHChangeNotify.Call(event, shcnfPathW, uintptr(unsafe.Pointer(encoded)), 0) //nolint:gosec // Audited Shell notification input.
	runtime.KeepAlive(encoded)
}

func comCall(object unsafe.Pointer, method int, args ...uintptr) uintptr {
	// COM objects expose a vtable pointer as their first machine word.
	vtable := *(*unsafe.Pointer)(object)
	methods := (*[32]uintptr)(vtable)
	callArgs := make([]uintptr, 0, len(args)+1)
	callArgs = append(callArgs, uintptr(object))
	callArgs = append(callArgs, args...)
	result, _, _ := syscall.SyscallN(methods[method], callArgs...)
	return result
}

func comRelease(object unsafe.Pointer) {
	_ = comCall(object, 2)
}

func failedHRESULT(value uintptr) bool {
	return int32(uint32(value)) < 0 //nolint:gosec // HRESULT is defined as a signed 32-bit value.
}

func hresultError(operation string, value uintptr) error {
	return fmt.Errorf("%s failed with HRESULT 0x%08X", operation, uint32(value)) //nolint:gosec // HRESULT is 32-bit.
}

func EscapePowerShellSingleQuoted(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}
