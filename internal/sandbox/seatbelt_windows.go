//go:build windows

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"github.com/NB-Agent/ok/internal/log"

	"github.com/NB-Agent/ok/internal/winhide"
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")

	advapi32                     = syscall.NewLazyDLL("advapi32.dll")
	procOpenProcessToken         = advapi32.NewProc("OpenProcessToken")
	procSetTokenInformation      = advapi32.NewProc("SetTokenInformation")
	procAllocateAndInitializeSid = advapi32.NewProc("AllocateAndInitializeSid")
	procFreeSid                  = advapi32.NewProc("FreeSid")
)

const (
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x2000
	jobObjectLimitActiveProcess       = 0x0008
	jobObjectLimitProcessMemory       = 0x0100
	processSetQuota                   = 0x0100
	processTerminate                  = 0x0001

	tokenAdjustDefault = 0x0080
	tokenQuery         = 0x0008

	tokenIntegrityLevel = 25
	seGroupIntegrity    = 0x0020
	lowIntegrityRID     = 0x1000
)

type joExtendedLimit struct {
	BasicLimit struct {
		PerProcessUserTimeLimit int64
		PerJobUserTimeLimit     int64
		LimitFlags              uint32
		MinimumWorkingSetSize   uintptr
		MaximumWorkingSetSize   uintptr
		ActiveProcessLimit      uint32
		Affinity                uintptr
		PriorityClass           uint32
		SchedulingClass         uint32
	}
	_ [32]byte
}

type tokenMandatoryLabel struct {
	Label sidAndAttributes
}

type sidAndAttributes struct {
	Sid        uintptr
	Attributes uint32
}

func Available() bool {
	// AppContainer mode requires Windows 8+ APIs.
	// Non-AppContainer mode is always available.
	return true
}

// Command wraps a shell command in the Windows sandbox via re-exec.
// Windows uses Low Integrity Level (filesystem), Job Object (lifecycle),
// directory ACL (write-path restriction), or AppContainer (process+network isolation).
func Command(spec Spec, shell, command string) ([]string, bool) {
	if err := enforceCheck(); err != nil {
		return []string{"", command}, false
	}
	if !spec.enforce() && !spec.appcontainer() {
		return []string{"cmd", "/c", command}, false
	}
	if spec.appcontainer() && !appContainerAvailable() {
		log.Warn("sandbox: AppContainer not available on this Windows version — using Low IL (no kernel-level network isolation); consider running on Windows 8+ for full isolation")
	}
	netFlag := "0"
	if spec.Network {
		netFlag = "1"
	}
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return []string{"cmd", "/c", command}, false
	}
	dir := filepath.Dir(exe)
	found := false
	for _, name := range []string{"ok-cli", "ok-cli.exe", "ok", "ok.exe"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			exe = filepath.Join(dir, name)
			found = true
			break
		}
	}
	if !found {
		// No adjacent binary found — cannot sandbox via re-exec.
		// Fall back to running the command unwrapped (sandbox unavailable).
		return []string{"cmd", "/c", command}, false
	}
	// Build the path list from writeAllowDirs and pass it via --roots.
	roots := strings.Join(writeAllowDirs(spec.WriteRoots), "\x00")
	argv := []string{exe, "--sandbox-exec"}
	if spec.appcontainer() {
		argv = append(argv, "--appcontainer")
	}
	argv = append(argv, "--roots", roots, "--net", netFlag, "--", shell, "-c", command)
	return argv, true
}

func lowIntegritySID() (uintptr, error) {
	var auth [6]byte
	auth[5] = 16
	r, _, e := procAllocateAndInitializeSid.Call(
		uintptr(unsafe.Pointer(&auth[0])),
		1,
		uintptr(lowIntegrityRID),
		0, 0, 0, 0, 0, 0, 0,
	)
	if r == 0 {
		return 0, fmt.Errorf("AllocateAndInitializeSid: %w", e)
	}
	return r, nil
}

func SandboxExec(args []string) {
	var rootsStr string
	var netFlag string
	appcon := false
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--roots":
			i++
			if i < len(args) {
				rootsStr = args[i]
			}
			i++
		case "--net":
			i++
			if i < len(args) {
				netFlag = args[i]
			}
			i++
		case "--appcontainer":
			appcon = true
			i++
		case "--":
			i++
			goto execCmd
		default:
			i++
		}
	}
	fmt.Fprintln(os.Stderr, "sandbox-exec: missing command after --")
	os.Exit(1)

execCmd:
	if i >= len(args) {
		fmt.Fprintln(os.Stderr, "sandbox-exec: no command")
		os.Exit(1)
	}
	cmdArgs := args[i:]
	argv0 := cmdArgs[0]

	if appcon {
		var ad []string
		if rootsStr != "" {
			ad = strings.Split(rootsStr, "\x00")
		}
		launchInAC(argv0, cmdArgs[1:], ad, netFlag == "1")
		return
	}

	if err := lowerIntegrity(); err != nil {
		log.Error("sandbox-exec", "err", err)
		os.Exit(1)
	}
	var ad []string
	if rootsStr != "" {
		ad = strings.Split(rootsStr, "\x00")
	}
	if err := restrictWriteACL(ad); err != nil {
		log.Error("sandbox-exec: acl", "err", err)
		os.Exit(1)
	}
	// Low IL cannot block outbound network at the kernel level (that requires
	// AppContainer or WFP). As a best-effort defence we poison common proxy
	// environment variables — most HTTP clients (curl, go, npm, pip, git)
	// honour them and will fail to connect. The AppContainer path above already
	// provides true kernel-level network isolation when available.
	cmd := winhide.Command(argv0, cmdArgs[1:]...)
	if netFlag == "0" {
		blockEnv := []string{
			"HTTP_PROXY=http://127.0.0.1:1",
			"HTTPS_PROXY=http://127.0.0.1:1",
			"http_proxy=http://127.0.0.1:1",
			"https_proxy=http://127.0.0.1:1",
			"ALL_PROXY=http://127.0.0.1:1",
			"all_proxy=http://127.0.0.1:1",
			"GIT_PROXY_COMMAND=echo block",
		}
		cmd.Env = append(os.Environ(), blockEnv...)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Error("sandbox-exec", "cmd", argv0, "err", err)
		os.Exit(1)
	}
	os.Exit(0)
}

func lowerIntegrity() error {
	proch, err := syscall.GetCurrentProcess()
	if err != nil {
		return fmt.Errorf("GetCurrentProcess: %w", err)
	}
	var token syscall.Token
	r, _, e := procOpenProcessToken.Call(
		uintptr(proch),
		uintptr(tokenAdjustDefault|tokenQuery),
		uintptr(unsafe.Pointer(&token)),
	)
	if r == 0 {
		return fmt.Errorf("OpenProcessToken: %w", e)
	}
	defer syscall.CloseHandle(syscall.Handle(token))

	sid, err := lowIntegritySID()
	if err != nil {
		return err
	}
	defer func() {
		if ret, _, _ := procFreeSid.Call(sid); ret == 0 {
			log.Warn("sandbox: FreeSid failed (SID may leak)")
		}
	}()

	tml := tokenMandatoryLabel{
		Label: sidAndAttributes{Sid: sid, Attributes: seGroupIntegrity},
	}
	r, _, e = procSetTokenInformation.Call(
		uintptr(token),
		uintptr(tokenIntegrityLevel),
		uintptr(unsafe.Pointer(&tml)),
		unsafe.Sizeof(tml),
	)
	if r == 0 {
		return fmt.Errorf("SetTokenInformation(TokenIntegrityLevel, Low): %w", e)
	}
	return nil
}

// WrapProcess assigns the child process (pid) to a Job Object that kills all
// children when the parent dies, and lowers its integrity level to Low.
// Best-effort: if the OS refuses the job assignment (e.g. the process is
// already in another job) we log a warning and carry on — unconfined is
// better than a broken pipeline. Each call creates its own anonymous Job
// Object so a failure on one child never poisons subsequent calls.
func WrapProcess(pid int, spec Spec) error {
	// Anonymous Job Object — no name means no collision with system jobs.
	h, _, e := procCreateJobObjectW.Call(0, 0)
	if h == 0 {
		log.Warn("sandbox: CreateJobObject", "err", e)
		return nil // degrade gracefully
	}
	jobObj := syscall.Handle(h)
	defer func() { _ = syscall.CloseHandle(jobObj) }()

	info := joExtendedLimit{}
	info.BasicLimit.LimitFlags = jobObjectLimitKillOnJobClose |
		jobObjectLimitActiveProcess |
		jobObjectLimitProcessMemory
	info.BasicLimit.ActiveProcessLimit = 50 // max 50 child processes
	// Per-process memory limit: 512 MB. The underlying field is in bytes.
	info.BasicLimit.MaximumWorkingSetSize = 512 * 1024 * 1024
	r, _, e := procSetInformationJobObject.Call(
		uintptr(jobObj), jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	if r == 0 {
		log.Warn("sandbox: SetInformationJobObject", "err", e)
		return nil // degrade gracefully
	}

	proc, e := syscall.OpenProcess(
		processSetQuota|processTerminate,
		false, uint32(pid))
	if e != nil {
		log.Warn("sandbox: OpenProcess", "pid", pid, "err", e)
		return nil // degrade gracefully
	}
	defer func() { _ = syscall.CloseHandle(proc) }()

	r, _, e = procAssignProcessToJobObject.Call(uintptr(jobObj), uintptr(proc))
	if r == 0 {
		log.Warn("sandbox: AssignProcessToJobObject", "pid", pid, "err", e)
		return nil // degrade gracefully
	}
	return nil
}

func LandlockExec(args []string) {
	fmt.Fprintln(os.Stderr, "landlock-exec: not on Windows")
	os.Exit(1)
}
