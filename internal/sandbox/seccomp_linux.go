//go:build linux

package sandbox

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

// sockFilter is a single BPF instruction (struct sock_filter in C).
type sockFilter struct {
	code uint16
	jt   uint8
	jf   uint8
	k    uint32
}

// sockFprog mirrors struct sock_fprog for PR_SET_SECCOMP.
type sockFprog struct {
	len    uint16
	filter *sockFilter
}

const (
	bpfLD  = 0x00
	bpfJMP = 0x05
	bpfRET = 0x06

	bpfW   = 0x00
	bpfABS = 0x20
	bpfJEQ = 0x10
)

const (
	seccompRetKill  = 0x80000000
	seccompRetAllow = 0x7fff0000
	seccompRetErrno = 0x00050000
)

const errnoEPERM = seccompRetErrno | 1

const (
	auditArchX86_64  = 0xC000003E
	auditArchAARCH64 = 0xC00000B7
)

// seccompFilter returns BPF bytecode blocking network-related syscalls.
// Layout (27 instructions total — see inline comments).
func seccompFilter() []sockFilter {
	_ = [27]struct{}{}

	f := make([]sockFilter, 27)

	// 0: LD [4] – load architecture
	f[0] = sockFilter{code: bpfLD | bpfW | bpfABS, k: 4}

	// 1: JEQ x86_64 → skip 2 to instr 4
	f[1] = sockFilter{code: bpfJMP | bpfJEQ, jt: 2, k: auditArchX86_64}

	// 2: JEQ aarch64 → skip 12 to instr 15
	f[2] = sockFilter{code: bpfJMP | bpfJEQ, jt: 12, k: auditArchAARCH64}

	// 3: RET KILL
	f[3] = sockFilter{code: bpfRET, k: seccompRetKill}

	// 4: LD [0] – load syscall number (x86_64 path: instrs 4-14)
	f[4] = sockFilter{code: bpfLD | bpfW | bpfABS, k: 0}

	// 5-13: x86_64 blocked syscalls, all jump to instr 26 (ERRNO)
	f[5] = sockFilter{code: bpfJMP | bpfJEQ, jt: 20, k: 41}   // socket
	f[6] = sockFilter{code: bpfJMP | bpfJEQ, jt: 19, k: 42}   // connect
	f[7] = sockFilter{code: bpfJMP | bpfJEQ, jt: 18, k: 43}   // accept
	f[8] = sockFilter{code: bpfJMP | bpfJEQ, jt: 17, k: 49}   // bind
	f[9] = sockFilter{code: bpfJMP | bpfJEQ, jt: 16, k: 50}   // listen
	f[10] = sockFilter{code: bpfJMP | bpfJEQ, jt: 15, k: 53}  // socketpair
	f[11] = sockFilter{code: bpfJMP | bpfJEQ, jt: 14, k: 288} // accept4
	f[12] = sockFilter{code: bpfJMP | bpfJEQ, jt: 13, k: 44}  // sendto
	f[13] = sockFilter{code: bpfJMP | bpfJEQ, jt: 12, k: 46}  // sendmsg

	// 14: RET ALLOW (x86_64)
	f[14] = sockFilter{code: bpfRET, k: seccompRetAllow}

	// 15: LD [0] – load syscall number (aarch64 path: instrs 15-25)
	f[15] = sockFilter{code: bpfLD | bpfW | bpfABS, k: 0}

	// 16-24: aarch64 blocked syscalls, all jump to instr 26 (ERRNO)
	f[16] = sockFilter{code: bpfJMP | bpfJEQ, jt: 9, k: 198} // socket
	f[17] = sockFilter{code: bpfJMP | bpfJEQ, jt: 8, k: 199} // socketpair
	f[18] = sockFilter{code: bpfJMP | bpfJEQ, jt: 7, k: 200} // bind
	f[19] = sockFilter{code: bpfJMP | bpfJEQ, jt: 6, k: 201} // listen
	f[20] = sockFilter{code: bpfJMP | bpfJEQ, jt: 5, k: 202} // accept
	f[21] = sockFilter{code: bpfJMP | bpfJEQ, jt: 4, k: 203} // connect
	f[22] = sockFilter{code: bpfJMP | bpfJEQ, jt: 3, k: 204} // accept4
	f[23] = sockFilter{code: bpfJMP | bpfJEQ, jt: 2, k: 211} // sendto (aarch64)
	f[24] = sockFilter{code: bpfJMP | bpfJEQ, jt: 1, k: 214} // sendmsg (aarch64)

	// 25: RET ALLOW (aarch64)
	f[25] = sockFilter{code: bpfRET, k: seccompRetAllow}

	// 26: RET ERRNO(EPERM) – all blocked syscalls land here
	f[26] = sockFilter{code: bpfRET, k: errnoEPERM}

	return f
}

// installNoNetworkFilter installs a seccomp-BPF filter that blocks network
// syscalls (socket, connect, bind, listen, accept, accept4, socketpair,
// sendto, sendmsg). Uses prctl(PR_SET_SECCOMP, SECCOMP_MODE_FILTER, &prog).
func installNoNetworkFilter() error {
	f := seccompFilter()
	prog := sockFprog{
		len:    uint16(len(f)),
		filter: &f[0],
	}
	const prSetSeccomp = 22
	const seccompModeFilter = 2
	if err := unix.Prctl(prSetSeccomp, seccompModeFilter, uintptr(unsafe.Pointer(&prog)), 0, 0); err != nil {
		return err
	}
	return nil
}
