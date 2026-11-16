package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func remoteTestWindowsSID(t *testing.T) *windows.SID {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	return user.User.Sid
}

func remoteTestWindowsSD(t *testing.T, sddl string) *windows.SECURITY_DESCRIPTOR {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	return sd
}

func remoteTestWindowsACL(t *testing.T, path, sddl string) {
	t.Helper()
	sd := remoteTestWindowsSD(t, sddl)
	owner, _, err := sd.Owner()
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		owner, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

func remoteTestWindowsPrivateSDDL(sid *windows.SID) string {
	return "O:" + sid.String() + "D:P(A;OICI;FA;;;" + sid.String() + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
}

func TestWindowsRemotePrivateDescriptorTrustsOnlyPrivatePrincipals(t *testing.T) {
	current := remoteTestWindowsSID(t)
	user := current.String()
	for _, tc := range []struct {
		name, descriptor string
		want             bool
	}{
		{"user", "O:" + user + "D:(A;;FA;;;" + user + ")", true},
		{"system owner", "O:SYD:(A;;FA;;;SY)(A;;FA;;;" + user + ")", true},
		{"administrators owner", "O:BAD:(A;;FA;;;BA)(A;;FA;;;" + user + ")", true},
		{"empty DACL", "O:" + user + "D:", true},
		{"null DACL", "O:" + user + "D:NO_ACCESS_CONTROL", false},
		{"missing DACL", "O:" + user, false},
		{"missing owner", "D:(A;;FA;;;SY)", false},
		{"untrusted owner", "O:WDD:(A;;FA;;;" + user + ")", false},
		{"everyone", "O:" + user + "D:(A;;FR;;;WD)", false},
		{"users", "O:" + user + "D:(A;;FW;;;BU)", false},
		{"authenticated users", "O:" + user + "D:(A;;FA;;;AU)", false},
		{"metadata grant", "O:" + user + "D:(A;;RC;;;WD)", false},
		{"synchronize grant", "O:" + user + "D:(A;;0x00100000;;;WD)", false},
		{"unknown mask", "O:" + user + "D:(A;;0x02000000;;;WD)", false},
		{"deny does not erase allow", "O:" + user + "D:(D;;FA;;;WD)(A;;FR;;;WD)", false},
		{"deny only", "O:" + user + "D:(D;;FA;;;WD)", true},
		{"zero grant", "O:" + user + "D:(A;;0x00000000;;;WD)", true},
		{"inherit only broad", "O:" + user + "D:(A;OICIIO;FR;;;WD)", false},
		{"inherited broad", "O:" + user + "D:(A;ID;FR;;;WD)", false},
		{"inherited private", "O:" + user + "D:(A;ID;FA;;;" + user + ")", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := privateRemoteDescriptor(remoteTestWindowsSD(t, tc.descriptor), current); got != tc.want {
				t.Fatalf("descriptor privacy = %v, want %v", got, tc.want)
			}
		})
	}
	safe := remoteTestWindowsSD(t, remoteTestWindowsPrivateSDDL(current))
	if privateRemoteDescriptor(nil, current) || privateRemoteDescriptor(new(windows.SECURITY_DESCRIPTOR), current) ||
		privateRemoteDescriptor(safe, nil) || privateRemoteDescriptor(safe, new(windows.SID)) ||
		privateRemoteSID(nil, current) || privateRemoteSID(current, nil) {
		t.Fatal("unknown descriptor or identity accepted")
	}
}

func TestWindowsRemotePrivateACERejectsUnknownFormatsAndMalformedSIDs(t *testing.T) {
	current := remoteTestWindowsSID(t)
	if privateRemoteACE(nil, current) || privateRemoteACE(new(windows.ACCESS_ALLOWED_ACE), current) {
		t.Fatal("missing or short ACE accepted")
	}
	for _, kind := range []byte{5, 9, 0xff} { // object, callback, unknown
		sd := remoteTestWindowsSD(t, "O:"+current.String()+"D:(A;;FA;;;"+current.String()+")")
		dacl, _, _ := sd.DACL()
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, 0, &ace); err != nil {
			t.Fatal(err)
		}
		ace.Header.AceType = kind
		if privateRemoteACE(ace, current) || privateRemoteDescriptor(sd, current) {
			t.Fatal("unknown ACE layout accepted")
		}
	}
	sd := remoteTestWindowsSD(t, "O:"+current.String()+"D:(A;;FA;;;WD)")
	dacl, _, _ := sd.DACL()
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatal(err)
	}
	ace.Header.AceSize = 16 // too short for the complete Everyone SID
	if privateRemoteACE(ace, current) {
		t.Fatal("SID extending past the ACE accepted")
	}
	*(*byte)(unsafe.Pointer(&ace.SidStart)) = 0 // invalid SID revision
	if privateRemoteACE(ace, current) {
		t.Fatal("invalid SID accepted")
	}
}

func TestWindowsRemoteCredentialAcceptsRealPrivateACLAndRejectsWideACL(t *testing.T) {
	current := remoteTestWindowsSID(t)
	root := t.TempDir()
	safe := remoteTestWindowsPrivateSDDL(current)
	remoteTestWindowsACL(t, root, safe)
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.RemoteDir(), 0700); err != nil {
		t.Fatal(err)
	}
	remoteTestWindowsACL(t, cfg.RemoteDir(), safe)
	path := cfg.remoteTokenPath()
	if err := os.WriteFile(path, []byte("synthetic-credential"), 0600); err != nil {
		t.Fatal(err)
	}
	remoteTestWindowsACL(t, path, safe)
	t.Cleanup(func() { remoteTestWindowsACL(t, path, safe) })
	info, _ := os.Lstat(path)
	if info.Mode().Perm()&0077 == 0 {
		t.Fatal("test must exercise real Windows mode bits that broke the old guard")
	}
	credential, err := cfg.ReadRemoteCredential()
	if err != nil || credential.Token != "synthetic-credential" {
		t.Fatal("safe Windows DACL prevented actual credential read")
	}
	for _, trustee := range []string{"WD", "BU", "AU"} {
		remoteTestWindowsACL(t, path, "O:"+current.String()+"D:P(A;;FA;;;"+current.String()+")(A;;FR;;;"+trustee+")")
		if _, err := cfg.ReadRemoteCredential(); err == nil {
			t.Fatal("broad real DACL released a credential")
		}
	}
	remoteTestWindowsACL(t, path, "O:"+current.String()+"D:NO_ACCESS_CONTROL")
	if _, err := cfg.ReadRemoteCredential(); err == nil {
		t.Fatal("NULL DACL released a real credential")
	}
	remoteTestWindowsACL(t, path, safe)
	if _, err := privateRemoteRead(filepath.Join(root, "missing")); !os.IsNotExist(err) {
		t.Fatal("missing credential error changed")
	}
	if privateRemotePermissions(filepath.Join(root, "missing"), nil) {
		t.Fatal("unreadable security descriptor accepted")
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", (16<<10)+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.ReadRemoteCredential(); err == nil {
		t.Fatal("oversized credential accepted")
	}
	if _, err := privateRemoteRead(cfg.RemoteDir()); err == nil {
		t.Fatal("directory accepted as a credential file")
	}
	link := filepath.Join(root, "credential-link")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := privateRemoteRead(link); err == nil {
		t.Fatal("symlink credential accepted")
	}
}

func TestWindowsRemoteStagingAndActivationUseRealPrivateACLs(t *testing.T) {
	current := remoteTestWindowsSID(t)
	root := t.TempDir()
	safe := remoteTestWindowsPrivateSDDL(current)
	remoteTestWindowsACL(t, root, safe)
	cfg, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	path, err := cfg.StageRemoteToken("synthetic-candidate", "https://gateway.example.invalid", "synthetic-public-key")
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.ActivateRemoteToken(path, RemoteCredential{Token: "synthetic-candidate", Origin: "https://gateway.example.invalid", Authorized: true, HostID: "host", UserID: "user"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.ReadRemoteCredential(); err != nil {
		t.Fatal(err)
	}
	file, err := os.CreateTemp(cfg.RemoteDir(), "empty-")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if !privateRemoteCreatedFile(file) {
		t.Fatal("new private file refused")
	}
	remoteTestWindowsACL(t, file.Name(), "O:"+current.String()+"D:P(A;;FA;;;"+current.String()+")(A;;FR;;;WD)")
	if privateRemoteCreatedFile(file) {
		t.Fatal("unsafe newly created file accepted before writing token bytes")
	}
	if info, _ := file.Stat(); info.Size() != 0 {
		t.Fatal("unsafe empty file acquired credential bytes")
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if privateRemoteCreatedFile(file) || privateRemoteCreatedFile(nil) {
		t.Fatal("closed or absent file accepted")
	}
	remoteTestWindowsACL(t, cfg.RemoteDir(), "O:"+current.String()+"D:P(A;OICI;FA;;;"+current.String()+")(A;OICIIO;FR;;;WD)")
	if _, err := cfg.StageRemoteToken("other", "origin", "public"); err == nil {
		t.Fatal("unsafe inherited rights accepted for a new credential")
	}
	if err := cfg.writeRemoteCredential(RemoteCredential{Token: "other"}); err == nil {
		t.Fatal("unsafe directory accepted for replacing a credential")
	}
	remoteTestWindowsACL(t, cfg.RemoteDir(), safe)
}
