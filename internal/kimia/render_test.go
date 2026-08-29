package kimia

import (
	"reflect"
	"strings"
	"testing"

	"github.com/harness-community/drone-kimia/internal/config"
)

func TestRenderBasicPush(t *testing.T) {
	t.Parallel()

	cfg := baseConfig()
	cfg.BuildArgs = []string{"VERSION=1.2.3", "MESSAGE=value with spaces,commas"}
	cfg.Labels = []string{"org.example.release=stable"}
	cfg.Target = "release"
	cfg.Platform = "linux/arm64"

	got, err := Render(cfg, []string{"registry.example.com/team/app:1.2.3", "registry.example.com/team/app:latest"})
	if err != nil {
		t.Fatal(err)
	}
	want := Command{
		Path: "/usr/local/bin/kimia",
		Args: []string{
			"--dockerfile=Dockerfile",
			"--context=/home/kimia/workspace",
			"--destination=registry.example.com/team/app:1.2.3",
			"--destination=registry.example.com/team/app:latest",
			"--build-arg=VERSION=1.2.3",
			"--build-arg=MESSAGE=value with spaces,commas",
			"--label=org.example.release=stable",
			"--target=release",
			"--custom-platform=linux/arm64",
			"--verbosity=info",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Render() = %#v, want %#v", got, want)
	}
}

func TestArgumentsBuildOnly(t *testing.T) {
	t.Parallel()
	cfg := baseConfig()
	cfg.NoPush = true
	cfg.DigestFile = "/home/kimia/results/digest"

	got, err := Arguments(cfg, []string{"example/app:latest"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--dockerfile=Dockerfile",
		"--context=/home/kimia/workspace",
		"--destination=example/app:latest",
		"--no-push",
		"--digest-file=/home/kimia/results/digest",
		"--verbosity=info",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Arguments() = %#v, want %#v", got, want)
	}
}

func TestArgumentsTarExport(t *testing.T) {
	t.Parallel()
	cfg := baseConfig()
	cfg.ContextSubPath = "services/api"
	cfg.TarPath = "/home/kimia/results/app.tar"
	cfg.ImageNameWithDigestFile = "/home/kimia/results/image-digest"

	got, err := Arguments(cfg, []string{"example/app:latest"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--dockerfile=Dockerfile",
		"--context=/home/kimia/workspace",
		"--context-sub-path=services/api",
		"--destination=example/app:latest",
		"--tar-path=/home/kimia/results/app.tar",
		"--image-name-with-digest-file=/home/kimia/results/image-digest",
		"--verbosity=info",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Arguments() = %#v, want %#v", got, want)
	}
}

func TestArgumentsCache(t *testing.T) {
	t.Parallel()
	cfg := baseConfig()
	cfg.EnableCache = true
	cfg.CacheRepo = "registry.example.com/team/app-cache:latest"
	cfg.ImportCache = []string{"type=local,src=/home/kimia/cache"}
	cfg.ExportCache = []string{"type=inline"}

	got, err := Arguments(cfg, []string{"example/app:latest"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--dockerfile=Dockerfile",
		"--context=/home/kimia/workspace",
		"--destination=example/app:latest",
		"--cache=true",
		"--import-cache=type=local,src=/home/kimia/cache",
		"--import-cache=type=registry,ref=registry.example.com/team/app-cache:latest",
		"--export-cache=type=inline",
		"--export-cache=type=registry,ref=registry.example.com/team/app-cache:latest,mode=max",
		"--verbosity=info",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Arguments() = %#v, want %#v", got, want)
	}
}

func TestArgumentsNormalizesDockerCacheImages(t *testing.T) {
	t.Parallel()
	cfg := baseConfig()
	cfg.ImportCache = []string{"registry.example.com/team/app:cache"}
	cfg.ExportCache = []string{"registry.example.com/team/app:cache-next"}

	got, err := Arguments(cfg, []string{"example/app:latest"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--dockerfile=Dockerfile",
		"--context=/home/kimia/workspace",
		"--destination=example/app:latest",
		"--cache=true",
		"--import-cache=type=registry,ref=registry.example.com/team/app:cache",
		"--export-cache=type=registry,ref=registry.example.com/team/app:cache-next,mode=max",
		"--verbosity=info",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Arguments() = %#v, want %#v", got, want)
	}
}

func TestArgumentsKimiaNativeBuildKitOptions(t *testing.T) {
	t.Parallel()
	cfg := baseConfig()
	cfg.Insecure = true
	cfg.InsecureRegistries = []string{"registry.example.com", "cache.example.com:5000"}
	cfg.LogTimestamp = true
	cfg.Reproducible = true
	cfg.Timestamp = "1700000000"
	cfg.GitBranch = "main"
	cfg.GitRevision = "abc123"
	cfg.GitTokenFile = "/home/kimia/secrets/git-token"
	cfg.GitTokenUser = "oauth2"
	cfg.Attest = []string{"type=sbom,generator=example/scanner:v1"}
	cfg.BuildKitOpts = []string{"build-arg:EXTRA=value"}
	cfg.Sign = true
	cfg.CosignKey = "/home/kimia/secrets/cosign.key"
	cfg.CosignPasswordEnv = "COSIGN_PASSWORD"

	got, err := Arguments(cfg, []string{"registry.example.com/team/app:latest"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--dockerfile=Dockerfile",
		"--context=/home/kimia/workspace",
		"--destination=registry.example.com/team/app:latest",
		"--insecure",
		"--insecure-registry=registry.example.com",
		"--insecure-registry=cache.example.com:5000",
		"--verbosity=info",
		"--log-timestamp",
		"--reproducible",
		"--timestamp=1700000000",
		"--git-branch=main",
		"--git-revision=abc123",
		"--git-token-file=/home/kimia/secrets/git-token",
		"--git-token-user=oauth2",
		"--attest=type=sbom,generator=example/scanner:v1",
		"--buildkit-opt=build-arg:EXTRA=value",
		"--sign",
		"--cosign-key=/home/kimia/secrets/cosign.key",
		"--cosign-password-env=COSIGN_PASSWORD",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Arguments() = %#v, want %#v", got, want)
	}
}

func TestArgumentsRejectsBuildKitNoOps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*config.Config)
		input  string
	}{
		{name: "cache dir", mutate: func(cfg *config.Config) { cfg.CacheDir = "/cache" }, input: "PLUGIN_CACHE_DIR"},
		{name: "insecure pull", mutate: func(cfg *config.Config) { cfg.InsecurePull = true }, input: "PLUGIN_INSECURE_PULL"},
		{name: "download retry", mutate: func(cfg *config.Config) { cfg.ImageDownloadRetry = 2 }, input: "PLUGIN_IMAGE_DOWNLOAD_RETRY"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := baseConfig()
			test.mutate(&cfg)
			_, err := Arguments(cfg, []string{"example/app:latest"})
			if err == nil || !strings.Contains(err.Error(), test.input) {
				t.Fatalf("Arguments() error = %v, want containing %q", err, test.input)
			}
		})
	}
}

func TestArgumentsRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		mutate  func(*config.Config)
		message string
	}{
		{name: "missing Dockerfile", mutate: func(cfg *config.Config) { cfg.Dockerfile = "" }, message: "Dockerfile path"},
		{name: "missing context", mutate: func(cfg *config.Config) { cfg.Context = "" }, message: "build context"},
		{name: "empty build arg key", mutate: func(cfg *config.Config) { cfg.BuildArgs = []string{"=bad"} }, message: "empty build argument"},
		{name: "label without value", mutate: func(cfg *config.Config) { cfg.Labels = []string{"invalid"} }, message: "key=value"},
		{name: "disabled cache with import", mutate: func(cfg *config.Config) {
			cfg.DisableCache = true
			cfg.ImportCache = []string{"type=registry,ref=example/cache"}
		}, message: "cache is disabled"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := baseConfig()
			test.mutate(&cfg)
			_, err := Arguments(cfg, []string{"example/app:latest"})
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Arguments() error = %v, want containing %q", err, test.message)
			}
		})
	}
}

func baseConfig() config.Config {
	return config.Config{
		KimiaPath:  "/usr/local/bin/kimia",
		Dockerfile: "Dockerfile",
		Context:    "/home/kimia/workspace",
		Verbosity:  "info",
	}
}
