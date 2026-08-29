package plugincli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/harness-community/drone-kimia/internal/app"
	"github.com/harness-community/drone-kimia/internal/config"
	"github.com/urfave/cli"
)

func TestApplyContextExportsDirectCLIValues(t *testing.T) {
	for _, key := range []string{
		"PLUGIN_EXPAND_TAG",
		"PLUGIN_BUILD_ARGS_NEW",
		"PLUGIN_MULTIPLE_BUILD_ARGS",
		"PLUGIN_CACHE_FROM",
		"PLUGIN_CACHE_TO",
		"PLUGIN_AUTO_TAG",
		"PLUGIN_NO_PUSH",
		"PLUGIN_PULL_IMAGE",
	} {
		t.Setenv(key, "")
	}

	flags := CommonFlags()
	application := cli.NewApp()
	application.Flags = flags
	application.Action = func(context *cli.Context) error {
		return ApplyContext(context, flags)
	}
	err := application.Run([]string{
		"drone-kimia",
		"--expand-tag",
		"--args-new", "FIRST=a,b;SECOND=two words",
		"--args-new", "THIRD=three",
		"--multiple-build-args",
		"--tags.auto",
		"--dry-run",
		"--cache-from", "type=local,src=/cache/one",
		"--cache-from", "type=registry,ref=registry.example/cache:two",
		"--cache-to", "type=local,dest=/cache/out",
		"--cache-to", "type=registry,ref=registry.example/cache:out,mode=max",
		"--pull-image=false",
	})
	if err != nil {
		t.Fatal(err)
	}

	assertEnvironment(t, "PLUGIN_EXPAND_TAG", "true")
	assertEnvironment(t, "PLUGIN_BUILD_ARGS_NEW", "FIRST=a,b;SECOND=two words;THIRD=three")
	assertEnvironment(t, "PLUGIN_MULTIPLE_BUILD_ARGS", "true")
	assertEnvironment(t, "PLUGIN_CACHE_FROM", "type=local,src=/cache/one;type=registry,ref=registry.example/cache:two")
	assertEnvironment(t, "PLUGIN_CACHE_TO", "type=local,dest=/cache/out;type=registry,ref=registry.example/cache:out,mode=max")
	assertEnvironment(t, "PLUGIN_AUTO_TAG", "true")
	assertEnvironment(t, "PLUGIN_NO_PUSH", "true")
	assertEnvironment(t, "PLUGIN_PULL_IMAGE", "false")
}

func TestRepeatableCacheFlagsRemainSeparateConfigSpecs(t *testing.T) {
	t.Setenv("PLUGIN_CACHE_FROM", "")
	t.Setenv("PLUGIN_REPO", "example/app")
	flags := CommonFlags()
	application := cli.NewApp()
	application.Flags = flags
	application.Action = func(context *cli.Context) error {
		return ApplyContext(context, flags)
	}
	if err := application.Run([]string{
		"drone-kimia",
		"--cache-from", "type=local,src=/cache/one",
		"--cache-from", "type=registry,ref=registry.example/cache:two",
	}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load("docker")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"type=local,src=/cache/one",
		"type=registry,ref=registry.example/cache:two",
	}
	if !reflect.DeepEqual(cfg.ImportCache, want) {
		t.Fatalf("cache imports = %#v, want %#v", cfg.ImportCache, want)
	}
}

func TestCommonFlagsExposeSupportedPluginInputs(t *testing.T) {
	expected := []string{
		"PLUGIN_ENV_FILE", "PLUGIN_DOCKERFILE", "PLUGIN_CONTEXT", "PLUGIN_CONTEXT_SUB_PATH",
		"PLUGIN_REPO", "PLUGIN_DESTINATIONS", "PLUGIN_EXPAND_REPO", "PLUGIN_TAG", "PLUGIN_TAGS",
		"PLUGIN_EXPAND_TAG", "PLUGIN_AUTO_TAG", "PLUGIN_DEFAULT_TAGS", "PLUGIN_AUTO_TAG_SUFFIX",
		"PLUGIN_DEFAULT_SUFFIX", "DRONE_COMMIT_REF", "DRONE_REPO_BRANCH", "PLUGIN_BUILD_ARGS",
		"PLUGIN_BUILD_ARGS_NEW", "PLUGIN_MULTIPLE_BUILD_ARGS", "PLUGIN_BUILD_ARGS_FROM_ENV",
		"PLUGIN_TARGET", "PLUGIN_CUSTOM_LABELS", "PLUGIN_PLATFORM", "PLUGIN_CUSTOM_PLATFORM",
		"PLUGIN_ENABLE_CACHE", "PLUGIN_NO_CACHE", "PLUGIN_CACHE_REPO", "PLUGIN_CACHE_FROM",
		"PLUGIN_CACHE_TO", "PLUGIN_IMPORT_CACHE", "PLUGIN_EXPORT_CACHE", "PLUGIN_NO_PUSH",
		"PLUGIN_DRY_RUN", "PLUGIN_TAR_PATH", "PLUGIN_DESTINATION_TAR_PATH", "PLUGIN_DIGEST_FILE",
		"PLUGIN_IMAGE_NAME_WITH_DIGEST_FILE", "PLUGIN_ARTIFACT_FILE", "DRONE_OUTPUT", "PLUGIN_INSECURE",
		"PLUGIN_INSECURE_REGISTRY", "PLUGIN_VERBOSITY", "PLUGIN_LOG_TIMESTAMP", "PLUGIN_REPRODUCIBLE",
		"PLUGIN_TIMESTAMP", "PLUGIN_GIT_BRANCH", "PLUGIN_GIT_REVISION", "PLUGIN_GIT_TOKEN_FILE",
		"PLUGIN_GIT_TOKEN_USER", "PLUGIN_ATTESTATION", "PLUGIN_ATTEST", "PLUGIN_BUILDKIT_OPT",
		"PLUGIN_SIGN", "PLUGIN_COSIGN_KEY", "PLUGIN_COSIGN_PASSWORD_ENV", "PLUGIN_PULL_IMAGE",
	}
	available := make(map[string]bool)
	for _, flag := range CommonFlags() {
		for _, environment := range strings.Split(flagEnvironment(flag), ",") {
			available[strings.TrimSpace(environment)] = true
		}
	}
	for _, environment := range expected {
		if !available[environment] {
			t.Errorf("common flags do not expose %s", environment)
		}
	}
}

func TestExecuteLoadsEnvFileBeforeCLIParsing(t *testing.T) {
	temporary := t.TempDir()
	environmentFile := filepath.Join(temporary, "plugin.env")
	if err := os.WriteFile(environmentFile, []byte("PLUGIN_REPO=from-file/example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	unsetEnvironment(t, "PLUGIN_ENV_FILE")
	unsetEnvironment(t, "PLUGIN_REPO")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(
		Options{Provider: "docker"},
		[]string{"drone-kimia", "--env-file", environmentFile, "--help"},
		app.Streams{Stdout: &stdout, Stderr: &stderr},
	)
	if code != 0 {
		t.Fatalf("Execute() code = %d, stderr = %q", code, stderr.String())
	}
	assertEnvironment(t, "PLUGIN_REPO", "from-file/example")
	if !strings.Contains(stdout.String(), "--expand-tag") || !strings.Contains(stdout.String(), "PLUGIN_EXPAND_TAG") {
		t.Fatalf("help does not expose the common plugin contract:\n%s", stdout.String())
	}
}

func TestExecuteHelpIncludesProviderFlags(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(
		Options{
			Provider: "ecr",
			ProviderFlags: []cli.Flag{
				cli.StringFlag{Name: "assume-role", EnvVar: "PLUGIN_ASSUME_ROLE"},
			},
		},
		[]string{"drone-kimia", "--help"},
		app.Streams{Stdout: &stdout, Stderr: &stderr},
	)
	if code != 0 {
		t.Fatalf("Execute() code = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "--assume-role") || !strings.Contains(stdout.String(), "PLUGIN_ASSUME_ROLE") {
		t.Fatalf("help does not expose provider inputs:\n%s", stdout.String())
	}
}

func TestHelpDoesNotPrintResolvedGenericValues(t *testing.T) {
	t.Setenv("PLUGIN_BUILD_ARGS_NEW", "SECRET=super-secret")
	t.Setenv("PLUGIN_BUILD_ARGS", "TOKEN=string-slice-secret")
	t.Setenv("PLUGIN_CACHE_TO", "type=registry,ref=user:password@registry.example/cache")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Execute(
		Options{Provider: "docker"},
		[]string{"drone-kimia", "--help"},
		app.Streams{Stdout: &stdout, Stderr: &stderr},
	)
	if code != 0 {
		t.Fatalf("Execute() code = %d, stderr = %q", code, stderr.String())
	}
	for _, secret := range []string{"super-secret", "string-slice-secret", "user:password"} {
		if strings.Contains(stdout.String(), secret) {
			t.Fatalf("help exposed resolved input %q:\n%s", secret, stdout.String())
		}
	}
}

func assertEnvironment(t *testing.T, key, expected string) {
	t.Helper()
	if actual := os.Getenv(key); actual != expected {
		t.Fatalf("%s = %q, want %q", key, actual, expected)
	}
}

func unsetEnvironment(t *testing.T, key string) {
	t.Helper()
	value, exists := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if exists {
			_ = os.Setenv(key, value)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func flagEnvironment(flag cli.Flag) string {
	switch typed := flag.(type) {
	case cli.StringFlag:
		return typed.EnvVar
	case cli.BoolFlag:
		return typed.EnvVar
	case cli.BoolTFlag:
		return typed.EnvVar
	case cli.IntFlag:
		return typed.EnvVar
	case cli.StringSliceFlag:
		return typed.EnvVar
	case cli.GenericFlag:
		return typed.EnvVar
	default:
		return ""
	}
}
