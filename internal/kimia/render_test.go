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
			"--cache=false",
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
		"--cache=false",
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
		"--cache=false",
		"--tar-path=/home/kimia/results/app.tar",
		"--image-name-with-digest-file=/home/kimia/results/image-digest",
		"--verbosity=info",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Arguments() = %#v, want %#v", got, want)
	}
}

func TestArgumentsTranslatesRegistryCacheForBuildah(t *testing.T) {
	t.Parallel()
	cfg := baseConfig()
	cfg.EnableCache = true
	cfg.CacheRepo = "registry.example.com/team/shared-cache:latest"
	cfg.ImportCache = []string{
		"registry.example.com/team/import-one:latest",
		"type=registry,ref=registry.example.com/team/import-two:latest,mode=min",
	}
	cfg.ExportCache = []string{
		"type=registry,ref=registry.example.com/team/export:latest,mode=max",
	}
	cfg.BuildahOpts = []string{"--squash", "--jobs 2"}

	got, err := Arguments(cfg, []string{"example/app:latest"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--dockerfile=Dockerfile",
		"--context=/home/kimia/workspace",
		"--destination=example/app:latest",
		"--cache=true",
		"--buildah-opt=--cache-from registry.example.com/team/import-one:latest",
		"--buildah-opt=--cache-from registry.example.com/team/import-two:latest",
		"--buildah-opt=--cache-from registry.example.com/team/shared-cache:latest",
		"--buildah-opt=--cache-to registry.example.com/team/export:latest",
		"--buildah-opt=--cache-to registry.example.com/team/shared-cache:latest",
		"--buildah-opt=--squash",
		"--buildah-opt=--jobs 2",
		"--verbosity=info",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Arguments() = %#v, want %#v", got, want)
	}
}

func TestArgumentsStableDeduplicatesBuildahCacheRepositories(t *testing.T) {
	t.Parallel()
	cfg := baseConfig()
	cfg.CacheRepo = "registry.example.com/team/cache:latest"
	cfg.ImportCache = []string{
		"registry.example.com/team/cache:latest",
		"type=registry,ref=registry.example.com/team/cache:latest,mode=max",
		"registry.example.com/team/second:latest",
	}
	cfg.ExportCache = []string{
		"type=registry,ref=registry.example.com/team/cache:latest,mode=max",
		"registry.example.com/team/second:latest",
		"registry.example.com/team/cache:latest",
	}

	got, err := Arguments(cfg, []string{"example/app:latest"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--dockerfile=Dockerfile",
		"--context=/home/kimia/workspace",
		"--destination=example/app:latest",
		"--cache=true",
		"--buildah-opt=--cache-from registry.example.com/team/cache:latest",
		"--buildah-opt=--cache-from registry.example.com/team/second:latest",
		"--buildah-opt=--cache-to registry.example.com/team/cache:latest",
		"--buildah-opt=--cache-to registry.example.com/team/second:latest",
		"--verbosity=info",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Arguments() = %#v, want %#v", got, want)
	}
}

func TestArgumentsRejectsUnsupportedCacheSpecifications(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		imports       []string
		exports       []string
		messagePieces []string
	}{
		{name: "local import", imports: []string{"type=local,src=/cache"}, messagePieces: []string{"type \"local\"", "only registry"}},
		{name: "inline export", exports: []string{"type=inline"}, messagePieces: []string{"type \"inline\"", "only registry"}},
		{name: "unknown type", imports: []string{"type=s3,ref=bucket"}, messagePieces: []string{"type \"s3\"", "only registry"}},
		{name: "missing ref", imports: []string{"type=registry,mode=max"}, messagePieces: []string{"missing ref"}},
		{name: "unsupported mode", exports: []string{"type=registry,ref=example/cache,mode=minimal"}, messagePieces: []string{"unsupported mode", "minimal"}},
		{name: "minimum export mode", exports: []string{"type=registry,ref=example/cache,mode=min"}, messagePieces: []string{"mode=min", "no Buildah equivalent"}},
		{name: "unknown attribute", exports: []string{"type=registry,ref=example/cache,compression=zstd"}, messagePieces: []string{"unsupported attribute", "compression"}},
		{name: "malformed attribute", imports: []string{"type=registry,ref"}, messagePieces: []string{"malformed"}},
		{name: "duplicate attribute", imports: []string{"type=registry,ref=one,ref=two"}, messagePieces: []string{"repeats attribute", "ref"}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := baseConfig()
			cfg.ImportCache = test.imports
			cfg.ExportCache = test.exports
			_, err := Arguments(cfg, []string{"example/app:latest"})
			if err == nil {
				t.Fatal("Arguments() accepted unsupported cache input")
			}
			for _, piece := range test.messagePieces {
				if !strings.Contains(err.Error(), piece) {
					t.Fatalf("Arguments() error = %q, want containing %q", err, piece)
				}
			}
		})
	}
}

func TestArgumentsKimiaNativeBuildahOptions(t *testing.T) {
	t.Parallel()
	cfg := baseConfig()
	cfg.StorageDriver = "vfs"
	cfg.Insecure = true
	cfg.InsecurePull = true
	cfg.InsecureRegistries = []string{"registry.example.com", "cache.example.com:5000"}
	cfg.ImageDownloadRetry = 3
	cfg.PushRetry = 4
	cfg.LogTimestamp = true
	cfg.Reproducible = true
	cfg.Timestamp = "1700000000"
	cfg.GitBranch = "main"
	cfg.GitRevision = "abc123"
	cfg.GitTokenFile = "/home/kimia/secrets/git-token"
	cfg.GitTokenUser = "oauth2"
	cfg.BuildahOpts = []string{"--squash"}

	got, err := Arguments(cfg, []string{"registry.example.com/team/app:latest"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--dockerfile=Dockerfile",
		"--context=/home/kimia/workspace",
		"--destination=registry.example.com/team/app:latest",
		"--storage-driver=vfs",
		"--cache=false",
		"--buildah-opt=--squash",
		"--insecure",
		"--insecure-pull",
		"--insecure-registry=registry.example.com",
		"--insecure-registry=cache.example.com:5000",
		"--image-download-retry=3",
		"--push-retry=4",
		"--verbosity=info",
		"--log-timestamp",
		"--reproducible",
		"--timestamp=1700000000",
		"--git-branch=main",
		"--git-revision=abc123",
		"--git-token-file=/home/kimia/secrets/git-token",
		"--git-token-user=oauth2",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Arguments() = %#v, want %#v", got, want)
	}
}

func TestArgumentsRejectsBuildKitOnlyInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*config.Config)
		input  string
	}{
		{name: "attestation", mutate: func(cfg *config.Config) { cfg.Attestation = "max" }, input: "PLUGIN_ATTESTATION"},
		{name: "attest", mutate: func(cfg *config.Config) { cfg.Attest = []string{"type=sbom"} }, input: "PLUGIN_ATTEST"},
		{name: "buildkit option", mutate: func(cfg *config.Config) { cfg.BuildKitOpts = []string{"build-arg:EXTRA=value"} }, input: "PLUGIN_BUILDKIT_OPT"},
		{name: "sign", mutate: func(cfg *config.Config) { cfg.Sign = true }, input: "PLUGIN_SIGN"},
		{name: "cosign key", mutate: func(cfg *config.Config) { cfg.CosignKey = "/secrets/cosign.key" }, input: "PLUGIN_COSIGN_KEY"},
		{name: "cosign password environment", mutate: func(cfg *config.Config) { cfg.CosignPasswordEnv = "COSIGN_PASSWORD" }, input: "PLUGIN_COSIGN_PASSWORD_ENV"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg := baseConfig()
			test.mutate(&cfg)
			_, err := Arguments(cfg, []string{"example/app:latest"})
			if err == nil || !strings.Contains(err.Error(), test.input) || !strings.Contains(err.Error(), "Buildah backend") {
				t.Fatalf("Arguments() error = %v, want Buildah rejection containing %q", err, test.input)
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
		{name: "cache directory", mutate: func(cfg *config.Config) { cfg.CacheDir = "/cache" }, message: "PLUGIN_CACHE_DIR"},
		{name: "native storage", mutate: func(cfg *config.Config) { cfg.StorageDriver = "native" }, message: "PLUGIN_STORAGE_DRIVER"},
		{name: "negative image retry", mutate: func(cfg *config.Config) { cfg.ImageDownloadRetry = -1 }, message: "PLUGIN_IMAGE_DOWNLOAD_RETRY"},
		{name: "negative push retry", mutate: func(cfg *config.Config) { cfg.PushRetry = -1 }, message: "PLUGIN_PUSH_RETRY"},
		{name: "empty buildah option", mutate: func(cfg *config.Config) { cfg.BuildahOpts = []string{" "} }, message: "PLUGIN_BUILDAH_OPT"},
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
