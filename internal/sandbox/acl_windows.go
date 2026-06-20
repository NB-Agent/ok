//go:build windows

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"

	"github.com/NB-Agent/ok/internal/log"
)

var (
	aclAdvapi32                = syscall.NewLazyDLL("advapi32.dll")
	procGetSecurityInfo        = aclAdvapi32.NewProc("GetSecurityInfo")
	procSetEntriesInAclW       = aclAdvapi32.NewProc("SetEntriesInAclW")
	procSetSecurityInfo        = aclAdvapi32.NewProc("SetSecurityInfo")
	procConvertStringSidToSidW = aclAdvapi32.NewProc("ConvertStringSidToSidW")

	aclKernel32   = syscall.NewLazyDLL("kernel32.dll")
	procLocalFree = aclKernel32.NewProc("LocalFree")
)

// SE_OBJECT_TYPE
const (
	seFileObject = 1
)

// SECURITY_INFORMATION flags
const (
	daclSecurityInformation = 0x0004
)

// ACCESS_MODE
const (
	grantAccess = 1
	denyAccess  = 3
)

// TRUSTEE_FORM / TRUSTEE_TYPE
const (
	trusteeIsSid            = 0
	trusteeIsWellKnownGroup = 5
)

// ACE inheritance flags
const (
	objectInheritAce    = 0x1
	containerInheritAce = 0x2
)

// Generic access rights
const (
	genericRead    = 0x80000000
	genericWrite   = 0x40000000
	genericExecute = 0x20000000
	genericAll     = 0x10000000
)

// Win32 error codes
const (
	errorSuccess            = 0
	errorNoSecurityOnObject = 135
)

// lowILString is the well-known Low Mandatory Level SID string.
const lowILString = "S-1-16-4096"

// explicitAccess mirrors Win32 EXPLICIT_ACCESS_W.
type explicitAccess struct {
	AccessPermissions uint32
	AccessMode        uint32
	Inheritance       uint32
	Trustee           trustee
}

// trustee mirrors Win32 TRUSTEE_W.
type trustee struct {
	MultipleTrustee          uintptr
	MultipleTrusteeOperation uint32
	TrusteeForm              uint32
	TrusteeType              uint32
	TrusteeName              uintptr
}

// restrictWriteACL grants Low IL write access to every directory in allowDirs
// (with child-object inheritance) and explicitly denies write access for Low IL
// on well-known system directories.
//
// Called from SandboxExec after lowerIntegrity() — the process already runs at
// Low IL, so by default it cannot write to Medium IL objects.  This function
// punches write holes for the workspace / temp / caches and seals system paths.
func restrictWriteACL(allowDirs []string) error {
	// Resolve the Low IL SID once.
	lowSID, err := convertStringSidToSid(lowILString)
	if err != nil {
		return fmt.Errorf("ConvertStringSidToSid(%s): %w", lowILString, err)
	}
	defer localFree(lowSID)

	// 1. Grant LOW IL read/write/execute (NOT genericAll which includes DELETE,
	//    WRITE_DAC, WRITE_OWNER — those would let the sandboxed process subvert
	//    the ACL and escape) on each allowed directory.
	for _, d := range allowDirs {
		d = filepath.Clean(d)
		if d == "" {
			continue
		}
		if _, err := os.Stat(d); os.IsNotExist(err) {
			continue // non-existent dirs in the list are harmless
		}
		if err := addDirectoryACE(d, lowSID, grantAccess, genericRead|genericWrite|genericExecute,
			objectInheritAce|containerInheritAce); err != nil {
			// Best-effort: log and move on.
			log.Warn("sandbox: acl grant", "dir", d, "err", err)
		}
	}

	// 2. Deny LOW IL GENERIC_WRITE on well-known system directories.
	for _, d := range criticalSystemDirectories() {
		if d == "" {
			continue
		}
		if _, err := os.Stat(d); os.IsNotExist(err) {
			continue
		}
		if err := addDirectoryACE(d, lowSID, denyAccess, genericWrite,
			objectInheritAce|containerInheritAce); err != nil {
			log.Warn("sandbox: acl deny", "dir", d, "err", err)
		}
	}

	return nil
}

// criticalSystemDirectories returns the list of well-known system paths that
// should be write-protected from Low IL processes.
func criticalSystemDirectories() []string {
	sysRoot := os.Getenv("SystemRoot")
	if sysRoot == "" {
		sysRoot = `C:\Windows`
	}
	progFiles := os.Getenv("ProgramFiles")
	if progFiles == "" {
		progFiles = `C:\Program Files`
	}
	progFilesX86 := os.Getenv("ProgramFiles(x86)")
	if progFilesX86 == "" {
		progFilesX86 = `C:\Program Files (x86)`
	}
	progData := os.Getenv("ProgramData")
	if progData == "" {
		progData = `C:\ProgramData`
	}
	allUsers := os.Getenv("ALLUSERSPROFILE")
	if allUsers == "" {
		allUsers = `C:\ProgramData`
	}
	dirs := []string{
		sysRoot,
		filepath.Join(sysRoot, "System32"),
		filepath.Join(sysRoot, "SysWOW64"),
		progFiles,
		progData,
		allUsers,
	}
	// Only add ProgramFiles(x86) if it differs from ProgramFiles.
	if progFilesX86 != "" && progFilesX86 != progFiles {
		dirs = append(dirs, progFilesX86)
	}
	return dirs
}

// addDirectoryACE modifies the DACL of the directory at path so that the given
// SID is granted or denied the specified access rights.  Inheritance flags
// control propagation to children.
func addDirectoryACE(path string, sid uintptr, accessMode, accessMask, inheritance uint32) error {
	// Open the directory with READ_CONTROL | WRITE_DAC.
	const requiredAccess = 0x00020000 | 0x00040000 // READ_CONTROL | WRITE_DAC
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("UTF16PtrFromString(%s): %w", path, err)
	}
	handle, err := syscall.CreateFile(
		pathPtr,
		requiredAccess,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_FLAG_BACKUP_SEMANTICS, // required for directories
		0,
	)
	if err != nil {
		return fmt.Errorf("CreateFile(%s): %w", path, err)
	}
	defer syscall.CloseHandle(handle)

	// Get current DACL.
	var pDacl uintptr
	var secDesc uintptr
	ret, _, e := procGetSecurityInfo.Call(
		uintptr(handle),
		seFileObject,
		daclSecurityInformation,
		0, // ppsidOwner – not needed
		0, // ppsidGroup – not needed
		uintptr(unsafe.Pointer(&pDacl)),
		0, // ppSacl – not needed
		uintptr(unsafe.Pointer(&secDesc)),
	)
	if ret != errorSuccess {
		if secDesc != 0 {
			localFree(secDesc)
		}
		return fmt.Errorf("GetSecurityInfo(%s): %w (err=0x%x)", path, e, ret)
	}
	// secDesc must be freed with LocalFree when no longer needed.
	defer func() {
		if secDesc != 0 {
			localFree(secDesc)
		}
	}()

	// Build the EXPLICIT_ACCESS entry.
	ea := explicitAccess{
		AccessPermissions: accessMask,
		AccessMode:        accessMode,
		Inheritance:       inheritance,
		Trustee: trustee{
			MultipleTrustee:          0,
			MultipleTrusteeOperation: 0, // NO_MULTIPLE_TRUSTEE
			TrusteeForm:              trusteeIsSid,
			TrusteeType:              trusteeIsWellKnownGroup,
			TrusteeName:              sid,
		},
	}

	// Merge into a new ACL.
	var newAcl uintptr
	ret, _, e = procSetEntriesInAclW.Call(
		uintptr(1),
		uintptr(unsafe.Pointer(&ea)),
		pDacl,
		uintptr(unsafe.Pointer(&newAcl)),
	)
	if ret != errorSuccess {
		return fmt.Errorf("SetEntriesInAclW(%s): %w (err=0x%x)", path, e, ret)
	}
	// newAcl must be freed with LocalFree.
	defer func() {
		if newAcl != 0 {
			localFree(newAcl)
		}
	}()

	// Apply the new DACL.
	ret, _, e = procSetSecurityInfo.Call(
		uintptr(handle),
		seFileObject,
		daclSecurityInformation,
		0, // psidOwner
		0, // psidGroup
		newAcl,
		0, // pSacl
	)
	if ret != errorSuccess {
		return fmt.Errorf("SetSecurityInfo(%s): %w (err=0x%x)", path, e, ret)
	}

	return nil
}

// convertStringSidToSid converts a SID string like "S-1-16-4096" to a SID
// pointer allocated by the Win32 API.  The caller must free it with localFree.
func convertStringSidToSid(sidStr string) (uintptr, error) {
	ws, err := syscall.UTF16PtrFromString(sidStr)
	if err != nil {
		return 0, err
	}
	var sid uintptr
	ret, _, e := procConvertStringSidToSidW.Call(
		uintptr(unsafe.Pointer(ws)),
		uintptr(unsafe.Pointer(&sid)),
	)
	if ret == 0 {
		return 0, fmt.Errorf("ConvertStringSidToSidW(%s): %w", sidStr, e)
	}
	return sid, nil
}

// localFree calls the Win32 LocalFree on a memory pointer allocated by
// GetSecurityInfo, SetEntriesInAclW, or ConvertStringSidToSidW.
func localFree(p uintptr) {
	ret, _, _ := procLocalFree.Call(p)
	if ret != 0 {
		log.Warn("sandbox: LocalFree returned non-zero", "ptr", p)
	}
}
