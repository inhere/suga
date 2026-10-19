//go:build windows

package process

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
	"unsafe"

	"github.com/gookit/goutil/x/assert"
	"golang.org/x/sys/windows"
)

func TestWindowsProcessErrorRequiresProofOfMissingPID(t *testing.T) {
	if processErrorMeansDead(windows.ERROR_ACCESS_DENIED) {
		t.Fatal("access denied must not classify a process as dead")
	}
	if !processErrorMeansDead(windows.ERROR_INVALID_PARAMETER) {
		t.Fatal("invalid PID must classify the process as dead")
	}
	if !processExists(4) {
		t.Fatal("Windows System PID 4 must remain conservatively alive")
	}
}

// assertOnlyOwnerAndSystem reads dir's DACL back and requires: protected, and every ACE
// is an allow ACE for the current user or SYSTEM, with the current user present.
func assertOnlyOwnerAndSystem(t *testing.T, dir string) {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	assert.Require(t, assert.NoErr(t, err))
	control, _, err := sd.Control()
	assert.Require(t, assert.NoErr(t, err))
	assert.True(t, control&windows.SE_DACL_PROTECTED != 0)
	acl, _, err := sd.DACL()
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.True(t, acl != nil))

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	assert.Require(t, assert.NoErr(t, err))
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	assert.Require(t, assert.NoErr(t, err))
	sawUser := false
	for i := uint32(0); i < uint32(acl.AceCount); i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		assert.Require(t, assert.NoErr(t, windows.GetAce(acl, i, &ace)))
		assert.Eq(t, uint8(windows.ACCESS_ALLOWED_ACE_TYPE), ace.Header.AceType)
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		switch {
		case sid.Equals(user.User.Sid):
			sawUser = true
		case sid.Equals(system):
		default:
			t.Fatalf("unexpected SID in DACL: %s", sid.String())
		}
	}
	assert.True(t, sawUser)
}

func TestWindowsSecureDirRestrictsFreshDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	assert.Require(t, assert.NoErr(t, os.MkdirAll(dir, 0o700)))
	assert.Require(t, assert.NoErr(t, secureDir(dir)))
	assertOnlyOwnerAndSystem(t, dir)
}

func TestWindowsSecureDirTightensExistingOpenDirectory(t *testing.T) {
	dir := t.TempDir()
	everyone, err := windows.CreateWellKnownSid(windows.WinWorldSid)
	assert.Require(t, assert.NoErr(t, err))
	open, err := windows.ACLFromEntries([]windows.EXPLICIT_ACCESS{{
		AccessPermissions: windows.GENERIC_ALL,
		AccessMode:        windows.GRANT_ACCESS,
		Inheritance:       windows.SUB_CONTAINERS_AND_OBJECTS_INHERIT,
		Trustee: windows.TRUSTEE{
			TrusteeForm:  windows.TRUSTEE_IS_SID,
			TrusteeType:  windows.TRUSTEE_IS_WELL_KNOWN_GROUP,
			TrusteeValue: windows.TrusteeValueFromSID(everyone),
		},
	}}, nil)
	assert.Require(t, assert.NoErr(t, err))
	assert.Require(t, assert.NoErr(t, windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION, nil, nil, open, nil)))

	assert.Require(t, assert.NoErr(t, secureDir(dir)))
	assertOnlyOwnerAndSystem(t, dir)
}

// TestWindowsStartSecuresRunDirectoryAndSocketWorks starts a session in a
// short home and checks the run directory's DACL.
func TestWindowsStartSecuresRunDirectoryAndSocketWorks(t *testing.T) {
	root := filepath.Join(shortWindowsTemp(t), "r")
	server, err := Start(root, "sess-acl", "acl", t.TempDir(), func(mode, text, from string) (Delivery, error) { return Delivery{}, nil })
	assert.Require(t, assert.NoErr(t, err))
	defer server.Close()
	assertOnlyOwnerAndSystem(t, root)
	_, err = Send(server.Record(), Request{Text: "hi", From: "test"})
	assert.NoErr(t, err)
}

// TestWindowsLongHomeFallsBackToSecuredDir: a too-long home starts through the
// per-user fallback directory, which carries the same protected DACL as the
// run root (the portable TestLongHomeUsesFallbackSocket checks the round trip).
func TestWindowsLongHomeFallsBackToSecuredDir(t *testing.T) {
	relocate(t)
	root := filepath.Join(shortWindowsTemp(t), strings.Repeat("long-segment-", 9))
	server, err := Start(root, "sess-fallback", "fallback", t.TempDir(), func(mode, text, from string) (Delivery, error) { return Delivery{}, nil })
	assert.Require(t, assert.NoErr(t, err))
	defer server.Close()
	assertOnlyOwnerAndSystem(t, fallbackDirPath())
	_, err = Send(server.Record(), Request{Text: "hi", From: "fallback"})
	assert.NoErr(t, err)
}

// shortWindowsTemp keeps the AF_UNIX path under the 100 UTF-16 unit limit.
func shortWindowsTemp(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "sg")
	assert.Require(t, assert.NoErr(t, err))
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestValidateSocketPathWindows pins the Windows AF_UNIX limit of 100 UTF-16 code units,
// including the reason it is measured in UTF-16 units: an emoji counts twice.
func TestValidateSocketPathWindows(t *testing.T) {
	t.Run("length at the limit is accepted", func(t *testing.T) {
		path := `C:\` + strings.Repeat("d", 97)
		assert.Eq(t, 100, len(utf16.Encode([]rune(path))))
		assert.NoErr(t, validateSocketPath(path))
	})
	t.Run("length over the limit is rejected", func(t *testing.T) {
		path := `C:\` + strings.Repeat("d", 98)
		err := validateSocketPath(path)
		// The message names the limit and the offending path: chat prints it to the user.
		assert.Err(t, err)
		assert.ErrSubMsg(t, err, "100")
	})
	t.Run("limit counts UTF-16 code units, not runes", func(t *testing.T) {
		// 50 astral runes are 100 code units (accepted), 51 are 102 (rejected).
		assert.NoErr(t, validateSocketPath(strings.Repeat("\U0001F600", 50)))
		assert.Err(t, validateSocketPath(strings.Repeat("\U0001F600", 51)))
	})
}
