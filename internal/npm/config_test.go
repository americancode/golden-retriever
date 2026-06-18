package npm

import (
	"context"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
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

func TestApplyEnvAuthForRegistryToken(t *testing.T) {
	t.Setenv("NPM_TARGET_TOKEN", "target-secret")
	cfg := DefaultConfig()
	cfg.ApplyEnvAuthForRegistry("https://gitlab.example/api/v4/projects/123/packages/npm")

	if got := cfg.AuthFor("https://gitlab.example/api/v4/projects/123/packages/npm/demo").Header; got != "Bearer target-secret" {
		t.Fatalf("auth = %s", got)
	}
}

func TestApplyEnvAuthForRegistryUserPassword(t *testing.T) {
	t.Setenv("CI_DEPLOY_USER", "deploy-user")
	t.Setenv("CI_DEPLOY_PASSWORD", "deploy-pass")
	cfg := DefaultConfig()
	cfg.ApplyEnvAuthForRegistry("https://gitlab.example/api/v4/projects/123/packages/npm")

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("deploy-user:deploy-pass"))
	if got := cfg.AuthFor("https://gitlab.example/api/v4/projects/123/packages/npm/demo").Header; got != want {
		t.Fatalf("auth = %s want %s", got, want)
	}
}

func TestApplyEnvAuthForRegistryEnvOverridesNPMRC(t *testing.T) {
	t.Setenv("NPM_TARGET_TOKEN", "env-secret")
	cfg := DefaultConfig()
	cfg.values["//gitlab.example/api/v4/projects/123/packages/npm/:_authToken"] = "npmrc-secret"
	cfg.ApplyEnvAuthForRegistry("https://gitlab.example/api/v4/projects/123/packages/npm")

	if got := cfg.AuthFor("https://gitlab.example/api/v4/projects/123/packages/npm/demo").Header; got != "Bearer env-secret" {
		t.Fatalf("auth = %s", got)
	}
}

func TestApplyEnvAuthForRegistryTokenPrecedence(t *testing.T) {
	t.Setenv("NPM_TARGET_TOKEN", "target")
	t.Setenv("NPM_AUTH_TOKEN", "auth")
	t.Setenv("NODE_AUTH_TOKEN", "node")
	t.Setenv("NPM_TOKEN", "npm")
	t.Setenv("CI_JOB_TOKEN", "job")
	cfg := DefaultConfig()
	cfg.ApplyEnvAuthForRegistry("https://gitlab.example/api/v4/projects/123/packages/npm")

	if got := cfg.AuthFor("https://gitlab.example/api/v4/projects/123/packages/npm/demo").Header; got != "Bearer target" {
		t.Fatalf("auth = %s", got)
	}
}

func TestApplyEnvAuthForRegistryCIJobTokenFallback(t *testing.T) {
	t.Setenv("CI_JOB_TOKEN", "job")
	cfg := DefaultConfig()
	cfg.ApplyEnvAuthForRegistry("https://gitlab.example/api/v4/projects/123/packages/npm")

	if got := cfg.AuthFor("https://gitlab.example/api/v4/projects/123/packages/npm/demo").Header; got != "Bearer job" {
		t.Fatalf("auth = %s", got)
	}
}

func TestApplyEnvAuthForRegistryUserPasswordPrecedence(t *testing.T) {
	t.Setenv("NPM_TARGET_USERNAME", "target-user")
	t.Setenv("NPM_TARGET_PASSWORD", "target-pass")
	t.Setenv("CI_DEPLOY_USER", "deploy-user")
	t.Setenv("CI_DEPLOY_PASSWORD", "deploy-pass")
	t.Setenv("NPM_USERNAME", "npm-user")
	t.Setenv("NPM_PASSWORD", "npm-pass")
	cfg := DefaultConfig()
	cfg.ApplyEnvAuthForRegistry("https://gitlab.example/api/v4/projects/123/packages/npm")

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("target-user:target-pass"))
	if got := cfg.AuthFor("https://gitlab.example/api/v4/projects/123/packages/npm/demo").Header; got != want {
		t.Fatalf("auth = %s want %s", got, want)
	}
}

func TestAuthForBareAndScopedAuth(t *testing.T) {
	cfg := DefaultConfig()
	cfg.values["//registry.example/:_auth"] = base64.StdEncoding.EncodeToString([]byte("root:secret"))
	cfg.values["//registry.example/@scope/:_authToken"] = "scoped-secret"

	if got := cfg.AuthFor("https://registry.example/left-pad").Header; got != "Basic "+base64.StdEncoding.EncodeToString([]byte("root:secret")) {
		t.Fatalf("bare auth = %s", got)
	}
	if got := cfg.AuthFor("https://registry.example/@scope/pkg").Header; got != "Bearer scoped-secret" {
		t.Fatalf("scoped auth = %s", got)
	}
}

func TestResolvePackageJSONRetriesLegacyPeerDepsOnERESOLVE(t *testing.T) {
	binDir := t.TempDir()
	projectDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "npm.log")

	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "` + logPath + `"
case " $* " in
  *" --legacy-peer-deps "*)
    cat > package-lock.json <<'EOF'
{"name":"fixture","lockfileVersion":3,"packages":{"":{"name":"fixture","version":"1.0.0"},"node_modules/left-pad":{"name":"left-pad","version":"1.3.0","resolved":"https://registry.npmjs.org/left-pad/-/left-pad-1.3.0.tgz"}}}
EOF
    exit 0
    ;;
esac
echo "npm error code ERESOLVE" >&2
echo "npm error ERESOLVE unable to resolve dependency tree" >&2
echo "npm error Fix the upstream dependency conflict, or retry this command with --force or --legacy-peer-deps to accept an incorrect (and potentially broken) dependency resolution." >&2
exit 1
`
	npmPath := filepath.Join(binDir, "npm")
	if err := os.WriteFile(npmPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "package.json"), []byte(`{"name":"fixture","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	oldStderr := os.Stderr
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = write
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(func() {
		os.Stderr = oldStderr
	})

	graph, err := LoadInput(context.Background(), nil, filepath.Join(projectDir, "package.json"), ResolveOptions{})
	if err != nil {
		t.Fatalf("LoadInput() error = %v", err)
	}
	if err := write.Close(); err != nil {
		t.Fatal(err)
	}
	stderrData, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}

	if !graph.Has("left-pad@1.3.0") {
		t.Fatalf("graph packages = %#v, want left-pad@1.3.0", graph.Packages())
	}
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(logData)), "\n")
	if len(lines) != 2 {
		t.Fatalf("npm calls = %q, want 2 invocations", string(logData))
	}
	if strings.Contains(lines[0], "--legacy-peer-deps") {
		t.Fatalf("first npm call = %q, should not include --legacy-peer-deps", lines[0])
	}
	if !strings.Contains(lines[1], "--legacy-peer-deps") {
		t.Fatalf("second npm call = %q, want --legacy-peer-deps", lines[1])
	}
	if !strings.Contains(string(stderrData), "retry=legacy-peer-deps") {
		t.Fatalf("stderr = %q, want legacy retry warning", string(stderrData))
	}
}
