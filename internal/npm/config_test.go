package npm

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigRegistryScopeAndAuth(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".npmrc")
	if err := os.WriteFile(path, []byte(`
registry=https://registry.example/
@scope:registry=https://scope.example/npm/
//registry.example/:_authToken=${NPM_TEST_TOKEN}
//scope.example/npm/:username=user
//scope.example/npm/:_password=`+base64.StdEncoding.EncodeToString([]byte("pass"))+`
`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NPM_TEST_TOKEN", "secret")

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.RegistryForPackage("left-pad"); got != "https://registry.example" {
		t.Fatalf("registry = %s", got)
	}
	if got := cfg.RegistryForPackage("@scope/pkg"); got != "https://scope.example/npm" {
		t.Fatalf("scope registry = %s", got)
	}
	if got := cfg.AuthFor("https://registry.example/left-pad").Header; got != "Bearer secret" {
		t.Fatalf("auth = %s", got)
	}
	wantBasic := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
	if got := cfg.AuthFor("https://scope.example/npm/@scope%2fpkg").Header; got != wantBasic {
		t.Fatalf("scoped auth = %s want %s", got, wantBasic)
	}
}
