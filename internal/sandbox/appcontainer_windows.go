//go:build windows

package sandbox

import (
	"fmt"
	"os"
	"strconv"
	"syscall"
	"unsafe"

	"github.com/NB-Agent/ok/internal/log"

	"github.com/NB-Agent/ok/internal/winhide"
)

var (
	userenv                                       = syscall.NewLazyDLL("userenv.dll")
	procCreateAppContainerProfile                 = userenv.NewProc("CreateAppContainerProfile")
	procDeleteAppContainerProfile                 = userenv.NewProc("DeleteAppContainerProfile")
	kernel32Proc                                  = syscall.NewLazyDLL("kernel32.dll")
	procDeriveAppContainerSidFromAppContainerName = kernel32Proc.NewProc("DeriveAppContainerSidFromAppContainerName")
	procInitializeProcThreadAttributeList         = kernel32Proc.NewProc("InitializeProcThreadAttributeList")
	procUpdateProcThreadAttribute                 = kernel32Proc.NewProc("UpdateProcThreadAttribute")
	procDeleteProcThreadAttributeList             = kernel32Proc.NewProc("DeleteProcThreadAttributeList")
)

const (
	procThreadAttributeSecurityCapabilities = 0x00020009
	extendedStartupInfoPresent              = 0x00080000
	// InternetClient capability SID (S-1-15-3-1) — allows outbound HTTP/HTTPS.
	internetClientCapSID = "S-1-15-3-1"
)

type securityCapabilities struct {
	AppContainerSid uint64
	Capabilities    uintptr
	CapabilityCount uint32
	Reserved        uint32
}

type startupInfoEx struct {
	StartupInfo   syscall.StartupInfo
	AttributeList uintptr
}

type processInfo struct {
	Process   syscall.Handle
	Thread    syscall.Handle
	ProcessID uint32
	ThreadID  uint32
}

var procCreateProcessW = kernel32.NewProc("CreateProcessW")

// convertStringSidToSidPtr converts a SID string (e.g. "S-1-15-3-1") to a uint64.
func convertStringSidToSidPtr(sidStr *uint16, sid *uint64) error {
	modadvapi32 := syscall.NewLazyDLL("advapi32.dll")
	proc := modadvapi32.NewProc("ConvertStringSidToSidW")
	r, _, e := proc.Call(uintptr(unsafe.Pointer(sidStr)), uintptr(unsafe.Pointer(sid)))
	if r == 0 {
		return e
	}
	return nil
}

func appContainerAvailable() bool {
	if err := procCreateAppContainerProfile.Find(); err != nil {
		return false
	}
	return procDeriveAppContainerSidFromAppContainerName.Find() == nil
}

func makeProfileName() string {
	return fmt.Sprintf("ok-sandbox-%s", strconv.FormatInt(int64(os.Getpid()), 16))
}

func createACProfile(name string, caps []string) (sid uint64, err error) {
	n, _ := syscall.UTF16PtrFromString(name)
	var ds uint64
	r, _, e := procDeriveAppContainerSidFromAppContainerName.Call(
		uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(&ds)))
	if r == 0 {
		return 0, fmt.Errorf("derive: %w", e)
	}
	d, _ := syscall.UTF16PtrFromString("OK")
	desc, _ := syscall.UTF16PtrFromString("sandbox")

	// Build capability SID list.
	var capPtrs []uint64
	for _, c := range caps {
		cStr, _ := syscall.UTF16PtrFromString(c)
		var sidVal uint64
		if err := convertStringSidToSidPtr(cStr, &sidVal); err == nil {
			capPtrs = append(capPtrs, sidVal)
		}
	}
	capData := uintptr(0)
	capCount := uint32(0)
	if len(capPtrs) > 0 {
		capData = uintptr(unsafe.Pointer(&capPtrs[0]))
		capCount = uint32(len(capPtrs))
	}

	r, _, e = procCreateAppContainerProfile.Call(
		uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(d)),
		uintptr(unsafe.Pointer(desc)), capData, uintptr(capCount), 0)
	if r != 0 {
		if r == 0x800700B7 {
			deleteACProfile(name)
			r, _, _ = procCreateAppContainerProfile.Call(
				uintptr(unsafe.Pointer(n)), uintptr(unsafe.Pointer(d)),
				uintptr(unsafe.Pointer(desc)), capData, uintptr(capCount), 0)
			if r != 0 {
				return 0, fmt.Errorf("create retry: 0x%x", r)
			}
		} else {
			return 0, fmt.Errorf("create: 0x%x: %w", r, e)
		}
	}
	return ds, nil
}

func deleteACProfile(name string) {
	n, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return
	}
	_, _, _ = procDeleteAppContainerProfile.Call(uintptr(unsafe.Pointer(n)))
}

func fallbackExec(a0 string, rest []string) {
	cmd := winhide.Command(a0, rest...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// Note: deleteACProfile must be called by the caller before fallbackExec
	// since os.Exit skips defers.
	if err := cmd.Run(); err != nil {
		log.Error("ac: fallback", "cmd", a0, "err", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// launchInAC runs the command inside a Windows AppContainer sandbox.
// The profile is deleted before every os.Exit or fallbackExec to avoid leaking
// AppContainer profiles (os.Exit skips deferred functions).
func launchInAC(a0 string, rest []string, allowDirs []string, allowNet bool) {
	nm := makeProfileName()

	// Add network capability when requested.
	var caps []string
	if allowNet {
		caps = append(caps, internetClientCapSID)
	}

	sid, err := createACProfile(nm, caps)
	if err != nil {
		log.Error("ac", "err", err, "info", "falling back")
		fmt.Fprintf(os.Stderr, "\n!! WARNING: AppContainer sandbox unavailable (%v)\n", err)
		fmt.Fprintln(os.Stderr, "   Commands will run WITHOUT sandbox isolation.")
		fmt.Fprintln(os.Stderr, "   Set sandbox.mode=\"enforce\" in ok.toml to block unconfined execution.")
		deleteACProfile(nm) // clean up before fallback (os.Exit skips defers)
		fallbackExec(a0, rest)
		return
	}
	// deleteACProfile is also deferred so it runs on the normal return path
	// (though in practice os.Exit below means only fallbackExec paths hit the defer).
	defer deleteACProfile(nm)

	if err := restrictWriteACL(allowDirs); err != nil {
		log.Error("ac: acl", "err", err)
	}

	cmdLine := syscall.EscapeArg(a0)
	for _, a := range rest {
		cmdLine += " " + syscall.EscapeArg(a)
	}

	si := startupInfoEx{}
	si.StartupInfo.Cb = uint32(unsafe.Sizeof(si))

	var sz uintptr
	procInitializeProcThreadAttributeList.Call(0, 1, 0, uintptr(unsafe.Pointer(&sz)))
	buf := make([]byte, sz)
	si.AttributeList = uintptr(unsafe.Pointer(&buf[0]))

	r, _, e := procInitializeProcThreadAttributeList.Call(si.AttributeList, 1, 0, uintptr(unsafe.Pointer(&sz)))
	if r == 0 {
		log.Error("ac: InitAttr", "err", e)
		fallbackExec(a0, rest)
		return
	}
	defer func() { procDeleteProcThreadAttributeList.Call(si.AttributeList) }()

	// Convert capability SID strings to uint64 pointers for the security
	// capabilities attribute. Must match createACProfile's conversion.
	var capPtrs []uint64
	for _, c := range caps {
		cStr, _ := syscall.UTF16PtrFromString(c)
		var sidVal uint64
		if err := convertStringSidToSidPtr(cStr, &sidVal); err == nil {
			capPtrs = append(capPtrs, sidVal)
		}
	}
	cp := securityCapabilities{
		AppContainerSid: sid,
		CapabilityCount: uint32(len(capPtrs)),
	}
	if len(capPtrs) > 0 {
		cp.Capabilities = uintptr(unsafe.Pointer(&capPtrs[0]))
	}

	r, _, e = procUpdateProcThreadAttribute.Call(si.AttributeList, 0,
		procThreadAttributeSecurityCapabilities,
		uintptr(unsafe.Pointer(&cp)), uintptr(unsafe.Sizeof(cp)), 0, 0)
	if r == 0 {
		log.Error("ac: UpdateAttr", "err", e)
		fallbackExec(a0, rest)
		return
	}

	cp2, _ := syscall.UTF16PtrFromString(cmdLine)
	var pi processInfo
	r, _, e = procCreateProcessW.Call(
		0,
		uintptr(unsafe.Pointer(cp2)),
		0,
		0,
		0,
		extendedStartupInfoPresent,
		0,
		0,
		uintptr(unsafe.Pointer(&si)),
		uintptr(unsafe.Pointer(&pi)),
	)
	if r == 0 {
		log.Error("ac: CreateProcess", "err", e)
		fallbackExec(a0, rest)
		return
	}

	syscall.CloseHandle(pi.Thread)
	_, _ = syscall.WaitForSingleObject(pi.Process, syscall.INFINITE)
	var ec uint32
	syscall.GetExitCodeProcess(pi.Process, &ec)
	syscall.CloseHandle(pi.Process)
	if ec != 0 {
		os.Exit(int(ec))
	}
	os.Exit(0)
}
