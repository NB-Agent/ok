//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/NB-Agent/ok/internal/log"
)

// landlockRulesetAttr mirrors struct landlock_ruleset_attr (ABI v1).
type landlockRulesetAttr struct {
	handledAccessFs uint64
}

// landlockPathBeneathAttr mirrors struct landlock_path_beneath_attr.
type landlockPathBeneathAttr struct {
	allowedAccess uint64
	parentFd      int32
	_             int32 // pad to 16 bytes
}

// All file-system access rights the sandbox handles — everything that mutates
// the filesystem. Reads and exec are deliberately NOT handled (they stay open
// so the toolchain — compilers, git, package managers — works freely).
const handledFS = unix.LANDLOCK_ACCESS_FS_WRITE_FILE |
	unix.LANDLOCK_ACCESS_FS_MAKE_REG |
	unix.LANDLOCK_ACCESS_FS_MAKE_DIR |
	unix.LANDLOCK_ACCESS_FS_MAKE_CHAR |
	unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
	unix.LANDLOCK_ACCESS_FS_MAKE_FIFO |
	unix.LANDLOCK_ACCESS_FS_MAKE_SOCK |
	unix.LANDLOCK_ACCESS_FS_MAKE_SYM |
	unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
	unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
	unix.LANDLOCK_ACCESS_FS_TRUNCATE |
	unix.LANDLOCK_ACCESS_FS_REFER |
	unix.LANDLOCK_ACCESS_FS_IOCTL_DEV

// Allowed access under permitted directories: everything.
const allowedFS = handledFS |
	unix.LANDLOCK_ACCESS_FS_READ_FILE |
	unix.LANDLOCK_ACCESS_FS_READ_DIR |
	unix.LANDLOCK_ACCESS_FS_EXECUTE

// Command returns args that re-exec the current binary as a landlock wrapper.
// The wrapper applies landlock, then execs the real command. The second return
// is always true when enforcement is active.
func Command(spec Spec, shell, command string) ([]string, bool) {
	if err := enforceCheck(); err != nil {
		return []string{"", command}, false
	}
	if !spec.enforce() || !Available() {
		return []string{shell, "-c", command}, false
	}
	roots := strings.Join(writeAllowDirs(spec.WriteRoots), "\x00")
	netFlag := "0"
	if spec.Network {
		netFlag = "1"
	}
	// Self-exec: /proc/self/exe --landlock-exec --roots <dirs> --net <0|1> -- <shell> -c <cmd>
	return []string{"/proc/self/exe", "--landlock-exec", "--roots", roots, "--net", netFlag, "--", shell, "-c", command}, true
}

// Available probes whether the kernel supports landlock (5.13+).
func Available() bool {
	attr := landlockRulesetAttr{handledAccessFs: 0}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET,
		uintptr(unsafe.Pointer(&attr)),
		unsafe.Sizeof(attr),
		0,
	)
	if errno != 0 {
		return false
	}
	unix.Close(int(fd))
	return true
}

// LandlockExec is the --landlock-exec re-exec entry point called from main().
// Parses args, applies landlock, then execs the target command. Never returns
// (calls os.Exit on error or execs on success).
func LandlockExec(args []string) {
	// Parse: --roots <dirs> --net <0|1> -- <cmd...>
	var roots []string
	network := false
	i := 0
	for i < len(args) {
		switch args[i] {
		case "--roots":
			i++
			if i < len(args) {
				roots = strings.Split(args[i], "\x00")
			}
		case "--net":
			i++
			if i < len(args) && args[i] == "1" {
				network = true
			}
		case "--":
			i++
			goto execCmd
		}
		i++
	}

	log.Error("landlock-exec: missing command after --")
	os.Exit(1)
	return

execCmd:
	if i >= len(args) {
		log.Error("landlock-exec: no command")
		os.Exit(1)
		return
	}

	// No new privs — required before landlock_restrict_self.
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		log.Error("landlock-exec: prctl(NO_NEW_PRIVS)", "err", err)
		os.Exit(1)
		return
	}

	// Network isolation: two complementary layers.
	//
	// 1. seccomp-BPF filter — blocks socket/connect/bind/listen/accept/accept4/
	//    socketpair syscalls.  This works without any capability and even inside
	//    containers that lack CAP_SYS_ADMIN.  Must be installed before fork
	//    (i.e. before any Go goroutines).  Best-effort: log and continue on
	//    failure.
	if !network {
		if err := installNoNetworkFilter(); err != nil {
			log.Warn("sandbox: seccomp filter install failed: " + err.Error())
		}
	}

	// 2. Network namespace — creates an isolated netns with only loopback.
	//    Requires CAP_SYS_ADMIN; best-effort — log and continue if the kernel
	//    refuses.
	if !network {
		if err := unix.Unshare(unix.CLONE_NEWNET); err != nil {
			log.Warn("landlock-exec: network isolation unavailable", "err", err)
		}
	}

	// Open each allowed directory and add a path_beneath rule.
	rulesetFD := createLandlockRuleset(network)
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		fd, err := unix.Open(root, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY, 0)
		if err != nil {
			// Non-existent dirs in the allow-list are harmless — the
			// command just can't create them. Log and continue.
			continue
		}
		addPathBeneathRule(rulesetFD, fd)
		unix.Close(fd)
	}

	// Apply the ruleset to this process. After this, all writes are
	// confined — even a fork+exec inherits the restriction.
	if err := landlockRestrictSelf(rulesetFD); err != nil {
		log.Error("landlock-exec: restrict_self", "err", err)
		unix.Close(rulesetFD)
		os.Exit(1)
		return
	}
	unix.Close(rulesetFD)

	// Apply resource limits before exec: fork bomb protection and
	// memory/large-alloc containment. Best-effort — log and continue.
	if err := unix.Setrlimit(unix.RLIMIT_NPROC, &unix.Rlimit{Cur: 50, Max: 50}); err != nil {
		log.Warn("landlock-exec: setrlimit(RLIMIT_NPROC)", "err", err)
	}
	if err := unix.Setrlimit(unix.RLIMIT_AS, &unix.Rlimit{Cur: 512 << 20, Max: 512 << 20}); err != nil {
		log.Warn("landlock-exec: setrlimit(RLIMIT_AS)", "err", err)
	}
	if err := unix.Setrlimit(unix.RLIMIT_NOFILE, &unix.Rlimit{Cur: 1024, Max: 1024}); err != nil {
		log.Warn("landlock-exec: setrlimit(RLIMIT_NOFILE)", "err", err)
	}
	// Exec the target command, replacing this process.
	cmdArgs := args[i:]
	if err := unix.Exec(cmdArgs[0], cmdArgs, os.Environ()); err != nil {
		log.Error("landlock-exec: exec", "cmd", cmdArgs[0], "err", err)
		os.Exit(1)
	}
}

func createLandlockRuleset(network bool) int {
	// Probe which file-system access rights the kernel supports.
	// LANDLOCK_ACCESS_FS_IOCTL_DEV requires Landlock ABI v4 (Linux ≥ 6.7).
	// On older kernels the flag is unknown and would make create_ruleset fail.
	probe := landlockRulesetAttr{handledAccessFs: handledFS}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET,
		uintptr(unsafe.Pointer(&probe)),
		unsafe.Sizeof(probe),
		0,
	)
	if errno == 0 {
		// Full ABI supported — close the probe fd and create the real ruleset.
		unix.Close(int(fd))
	} else {
		// Try without IOCTL_DEV (ABI v3 or older).
		stripped := handledFS &^ unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
		probe2 := landlockRulesetAttr{handledAccessFs: stripped}
		fd2, _, errno2 := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET,
			uintptr(unsafe.Pointer(&probe2)),
			unsafe.Sizeof(probe2),
			0,
		)
		if errno2 != 0 {
			log.Error("landlock-exec: create_ruleset (stripped)", "err", errno2.Error())
			os.Exit(1)
		}
		return int(fd2)
	}

	attr := landlockRulesetAttr{handledAccessFs: handledFS}
	fd, _, errno = unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET,
		uintptr(unsafe.Pointer(&attr)),
		unsafe.Sizeof(attr),
		0,
	)
	if errno != 0 {
		log.Error("landlock-exec: create_ruleset", "err", errno.Error())
		os.Exit(1)
	}
	return int(fd)
}

func addPathBeneathRule(rulesetFD, dirFD int) {
	attr := landlockPathBeneathAttr{
		allowedAccess: allowedFS,
		parentFd:      int32(dirFD),
	}
	_, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE,
		uintptr(rulesetFD),
		uintptr(unix.LANDLOCK_RULE_PATH_BENEATH),
		uintptr(unsafe.Pointer(&attr)),
		0, 0, 0,
	)
	if errno != 0 {
		log.Warn("landlock-exec: add_rule", "err", errno.Error())
		// Non-fatal: the dir just won't be writable.
	}
}

func landlockRestrictSelf(rulesetFD int) error {
	_, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF,
		uintptr(rulesetFD),
		0, 0,
	)
	if errno != 0 {
		return errno
	}
	return nil
}
