package remote

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LubyRuffy/eino-swarm/internal/config"
	"github.com/LubyRuffy/eino-swarm/internal/provider"
	"golang.org/x/sys/windows"
)

func accountWindowsACL(t *testing.T, path, sddl string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
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

// Exercise real persisted credentials through AccountModels' HTTP adapters.
// A helper-only ACL test cannot prove that safe Windows login reaches a catalog
// or that losing credential privacy revokes the previously published source.
func TestWindowsPrivateGatewayCredentialReachesHTTPAndWideACLRevokesModels(t *testing.T) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := user.User.Sid.String()
	safe := "O:" + sid + "D:P(A;OICI;FA;;;" + sid + ")(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"
	root := t.TempDir()
	accountWindowsACL(t, root, safe)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	g := newAccountGateway(t)
	if err := cfg.Update(func(next *config.Config) error {
		next.Remote.AuthMode = config.RemoteAuthAccount
		next.Remote.HubURL = g.server.URL
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.RemoteDir(), 0700); err != nil {
		t.Fatal(err)
	}
	accountWindowsACL(t, cfg.RemoteDir(), safe)
	raw, err := json.Marshal(config.RemoteCredential{
		Schema: 1, Token: accountModelsToken, Origin: g.server.URL, Authorized: true,
		HostID: strings.Repeat("1", 32), UserID: strings.Repeat("2", 32), Username: "Account",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(cfg.RemoteDir(), "host_token")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	accountWindowsACL(t, path, safe)
	pool := provider.New(cfg)
	account := NewAccountModels(cfg, pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if state := account.Refresh(t.Context()); state.State != AccountReady || pool.AccountSourceID() != accountModelsSourceID || len(g.seen()) != 2 {
		t.Fatal("private Windows credential did not reach the real bootstrap/catalog HTTP adapters")
	}
	for _, path := range g.seen() {
		if path != "/client/desktop/v1/bootstrap" && path != "/client/desktop/v1/models" {
			t.Fatal("account refresh reached an unexpected route")
		}
	}
	before := len(g.seen())
	accountWindowsACL(t, path, "O:"+sid+"D:P(A;;FA;;;"+sid+")(A;;FR;;;WD)")
	if state := account.Refresh(t.Context()); state.State != AccountSignedOut || pool.AccountSourceID() != "" || len(g.seen()) != before {
		t.Fatal("unsafe Windows credential retained models or sent another gateway request")
	}
	accountWindowsACL(t, path, safe)
	if state := account.Refresh(t.Context()); state.State != AccountReady || pool.AccountSourceID() != accountModelsSourceID || len(g.seen()) != before+2 {
		t.Fatal("restored private credential could not refresh its gateway models")
	}
}
