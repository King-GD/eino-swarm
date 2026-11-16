package config

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows reports 0666/0777 regardless of a file's private DACL. Inspect the
// security descriptor instead; never change an existing object's permissions.
func privateRemotePermissions(path string, _ os.FileInfo) bool {
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return false
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return false
	}
	return privateRemoteDescriptor(sd, user.User.Sid)
}

func privateRemoteDescriptor(sd *windows.SECURITY_DESCRIPTOR, current *windows.SID) bool {
	if sd == nil || !sd.IsValid() || current == nil || !current.IsValid() {
		return false
	}
	owner, _, err := sd.Owner()
	if err != nil || !privateRemoteSID(owner, current) {
		return false
	}
	dacl, _, err := sd.DACL()
	// NULL/absent DACLs grant everyone access. A non-nil zero-ACE DACL grants
	// nothing; the real file operation still decides whether our access works.
	if err != nil || dacl == nil {
		return false
	}
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if windows.GetAce(dacl, index, &ace) != nil || !privateRemoteACE(ace, current) {
			return false
		}
	}
	return true
}

func privateRemoteSID(sid, current *windows.SID) bool {
	return current != nil && current.IsValid() && sid != nil && sid.IsValid() && (sid.Equals(current) ||
		sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid))
}

func privateRemoteACE(ace *windows.ACCESS_ALLOWED_ACE, current *windows.SID) bool {
	// Only the two ordinary ACE formats have the SID layout we inspect. Unknown,
	// object and callback ACEs refuse rather than guessing their access rules.
	const sidOffset = unsafe.Offsetof(windows.ACCESS_ALLOWED_ACE{}.SidStart)
	if ace == nil || uintptr(ace.Header.AceSize) < sidOffset+8 ||
		(ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE && ace.Header.AceType != windows.ACCESS_DENIED_ACE_TYPE) {
		return false
	}
	// Bound the SID header's declared sub-authorities before passing its pointer
	// to any SID API. Ordinary ACEs carry an 8-byte SID header followed by DWORDs.
	header := (*[8]byte)(unsafe.Pointer(&ace.SidStart))
	if header[0] != 1 || header[1] > 15 || 8+4*uintptr(header[1]) > uintptr(ace.Header.AceSize)-sidOffset {
		return false
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !sid.IsValid() {
		return false
	}
	// A deny cannot compensate for an untrusted allow elsewhere. Inspect
	// inherit-only grants too: credential temp files can inherit those grants.
	return ace.Header.AceType == windows.ACCESS_DENIED_ACE_TYPE || ace.Mask == 0 || privateRemoteSID(sid, current)
}
