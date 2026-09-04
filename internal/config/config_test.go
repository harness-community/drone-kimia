package config

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestLoadTagsFileSupportsNewlines(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile(".tags", []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PLUGIN_REPO", "example/app")
	t.Setenv("PLUGIN_TAG", "")
	t.Setenv("PLUGIN_TAGS", "")
	cfg, err := Load("docker")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"one", "two"}; !reflect.DeepEqual(cfg.Tags, want) {
		t.Fatalf("tags = %#v, want %#v", cfg.Tags, want)
	}
}

func TestLoadBuildOnlyStillRequiresRepo(t *testing.T) {
	t.Setenv("PLUGIN_NO_PUSH", "true")
	t.Setenv("PLUGIN_REPO", "")
	_, err := Load("docker")
	if err == nil || !strings.Contains(err.Error(), "PLUGIN_REPO or PLUGIN_DESTINATIONS is required") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestLoadAcceptsNativeDestinationsWithoutRepo(t *testing.T) {
	t.Setenv("PLUGIN_DESTINATIONS", "registry.example/team/app:one;registry.example/team/app:two")
	cfg, err := Load("docker")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"registry.example/team/app:one", "registry.example/team/app:two"}
	if !reflect.DeepEqual(cfg.Destinations, want) {
		t.Fatalf("destinations = %#v, want %#v", cfg.Destinations, want)
	}
}

func TestNativeDestinationsBypassUnrelatedTagResolution(t *testing.T) {
	t.Setenv("PLUGIN_DESTINATIONS", "registry.example/team/app:one")
	t.Setenv("PLUGIN_AUTO_TAG", "true")
	t.Setenv("PLUGIN_EXPAND_TAG", "true")
	cfg, err := Load("docker")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Tags) != 0 {
		t.Fatalf("native destinations unexpectedly resolved tags: %#v", cfg.Tags)
	}
}

func TestNativeDestinationsRejectRepositoryMode(t *testing.T) {
	t.Setenv("PLUGIN_REPO", "example/app")
	t.Setenv("PLUGIN_DESTINATIONS", "registry.example/team/app:one")
	_, err := Load("docker")
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestCloudProviderDoesNotUseBaseDockerRegistryAsDestination(t *testing.T) {
	t.Setenv("PLUGIN_REPO", "team/app")
	t.Setenv("DOCKER_REGISTRY", "base-images.example")
	cfg, err := Load("ecr")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Registry != "" {
		t.Fatalf("cloud destination registry = %q, want empty", cfg.Registry)
	}
}

func TestDockerProviderKeepsDockerRegistryDestinationAlias(t *testing.T) {
	t.Setenv("PLUGIN_REPO", "team/app")
	t.Setenv("DOCKER_REGISTRY", "push.example")
	cfg, err := Load("docker")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Registry != "push.example" {
		t.Fatalf("Docker destination registry = %q", cfg.Registry)
	}
}

func TestLoadAcceptsRelativeHarnessTarPath(t *testing.T) {
	t.Setenv("PLUGIN_REPO", "example/app")
	t.Setenv("PLUGIN_DESTINATION_TAR_PATH", "imageci.tar")
	cfg, err := Load("docker")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TarPath != "imageci.tar" {
		t.Fatalf("tar path = %q", cfg.TarPath)
	}
}

func TestLoadPushOnly(t *testing.T) {
	t.Setenv("PLUGIN_REPO", "example/app")
	t.Setenv("PLUGIN_TAGS", "test")
	t.Setenv("PLUGIN_PUSH_ONLY", "true")
	t.Setenv("PLUGIN_SOURCE_TAR_PATH", "imageci.tar")
	cfg, err := Load("docker")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.PushOnly || cfg.SourceTarPath != "imageci.tar" {
		t.Fatalf("unexpected push-only config: %#v", cfg)
	}
}

func TestLoadPushOnlyRequiresSourceTar(t *testing.T) {
	t.Setenv("PLUGIN_REPO", "example/app")
	t.Setenv("PLUGIN_PUSH_ONLY", "true")
	_, err := Load("docker")
	if err == nil || !strings.Contains(err.Error(), "PLUGIN_SOURCE_TAR_PATH is required") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestLoadRejectsPushOnlyWithNoPush(t *testing.T) {
	t.Setenv("PLUGIN_REPO", "example/app")
	t.Setenv("PLUGIN_PUSH_ONLY", "true")
	t.Setenv("PLUGIN_SOURCE_TAR_PATH", "imageci.tar")
	t.Setenv("PLUGIN_NO_PUSH", "true")
	_, err := Load("docker")
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestLoadCacheConflict(t *testing.T) {
	t.Setenv("PLUGIN_REPO", "example/app")
	t.Setenv("PLUGIN_ENABLE_CACHE", "true")
	t.Setenv("PLUGIN_NO_CACHE", "true")
	_, err := Load("docker")
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestUnsupportedFalseIsIgnored(t *testing.T) {
	t.Setenv("PLUGIN_REPO", "example/app")
	t.Setenv("PLUGIN_SQUASH", "false")
	if _, err := Load("docker"); err != nil {
		t.Fatal(err)
	}
}

func TestLoadCacheFromIsCommaDelimited(t *testing.T) {
	t.Setenv("PLUGIN_REPO", "example/app")
	t.Setenv("PLUGIN_CACHE_FROM", "example/cache:one,example/cache:two")
	cfg, err := Load("docker")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"type=registry,ref=example/cache:one", "type=registry,ref=example/cache:two"}
	if !reflect.DeepEqual(cfg.ImportCache, want) {
		t.Fatalf("cache imports = %#v, want %#v", cfg.ImportCache, want)
	}
}

func TestLoadBuildxCacheSpecPreservesCommas(t *testing.T) {
	t.Setenv("PLUGIN_REPO", "example/app")
	t.Setenv("PLUGIN_CACHE_FROM", "type=registry,ref=example/cache,mode=max;type=local,src=/cache")
	cfg, err := Load("docker")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"type=registry,ref=example/cache,mode=max", "type=local,src=/cache"}
	if !reflect.DeepEqual(cfg.ImportCache, want) {
		t.Fatalf("cache imports = %#v, want %#v", cfg.ImportCache, want)
	}
}

func TestLoadBuildArgsFromEnvironmentSkipsEmptyAndUsesHarnessAlias(t *testing.T) {
	clearProxyEnvironment(t)
	t.Setenv("PLUGIN_REPO", "example/app")
	t.Setenv("PLUGIN_BUILD_ARGS_FROM_ENV", "missing,token")
	t.Setenv("HARNESS_TOKEN", "value")
	cfg, err := Load("docker")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"token=value"}; !reflect.DeepEqual(cfg.BuildArgs, want) {
		t.Fatalf("build args = %#v, want %#v", cfg.BuildArgs, want)
	}
}

func TestBuildArgEnvironmentDoesNotOverrideExplicitValue(t *testing.T) {
	clearProxyEnvironment(t)
	t.Setenv("PLUGIN_REPO", "example/app")
	t.Setenv("PLUGIN_BUILD_ARGS", "TOKEN=explicit")
	t.Setenv("PLUGIN_BUILD_ARGS_FROM_ENV", "TOKEN")
	t.Setenv("TOKEN", "environment")
	cfg, err := Load("docker")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"TOKEN=explicit"}; !reflect.DeepEqual(cfg.BuildArgs, want) {
		t.Fatalf("build args = %#v, want %#v", cfg.BuildArgs, want)
	}
}

func clearProxyEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"http_proxy", "HTTP_PROXY", "HARNESS_HTTP_PROXY",
		"https_proxy", "HTTPS_PROXY", "HARNESS_HTTPS_PROXY",
		"no_proxy", "NO_PROXY", "HARNESS_NO_PROXY",
	} {
		t.Setenv(key, "")
	}
}

func TestHarnessCAPathIsIgnored(t *testing.T) {
	t.Setenv("PLUGIN_REPO", "example/app")
	t.Setenv("HARNESS_CA_PATH", "/platform/injected/ca.pem")
	if _, err := Load("docker"); err != nil {
		t.Fatal(err)
	}
}

func TestPlatformMetadataEnvironmentIsIgnored(t *testing.T) {
	t.Setenv("PLUGIN_REPO", "example/app")
	t.Setenv("DRONE_REPO_LINK", "https://example.invalid/team/repo")
	t.Setenv("DRONE_CARD_PATH", "/harness/card.json")
	if _, err := Load("docker"); err != nil {
		t.Fatal(err)
	}
}

func TestLoadBuildahInputs(t *testing.T) {
	t.Setenv("PLUGIN_REPO", "example/app")
	t.Setenv("PLUGIN_STORAGE_DRIVER", "VFS")
	t.Setenv("PLUGIN_INSECURE_PULL", "true")
	t.Setenv("PLUGIN_IMAGE_DOWNLOAD_RETRY", "3")
	t.Setenv("PLUGIN_PUSH_RETRY", "4")
	t.Setenv("PLUGIN_BUILDAH_OPT", "--squash;--jobs 2")

	cfg, err := Load("docker")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StorageDriver != "vfs" || !cfg.InsecurePull || cfg.ImageDownloadRetry != 3 || cfg.PushRetry != 4 {
		t.Fatalf("unexpected Buildah config: %#v", cfg)
	}
	if want := []string{"--squash", "--jobs 2"}; !reflect.DeepEqual(cfg.BuildahOpts, want) {
		t.Fatalf("Buildah options = %#v, want %#v", cfg.BuildahOpts, want)
	}
}

func TestLoadRejectsInvalidBuildahInputs(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		value   string
		message string
	}{
		{name: "native storage", key: "PLUGIN_STORAGE_DRIVER", value: "native", message: "vfs or overlay"},
		{name: "unknown storage", key: "PLUGIN_STORAGE_DRIVER", value: "zfs", message: "vfs or overlay"},
		{name: "negative pull retry", key: "PLUGIN_IMAGE_DOWNLOAD_RETRY", value: "-1", message: "nonnegative"},
		{name: "negative push retry", key: "PLUGIN_PUSH_RETRY", value: "-1", message: "nonnegative"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("PLUGIN_REPO", "example/app")
			t.Setenv(test.key, test.value)
			_, err := Load("docker")
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Load() error = %v, want containing %q", err, test.message)
			}
		})
	}
}

func TestLoadStillRejectsCacheDirectory(t *testing.T) {
	t.Setenv("PLUGIN_REPO", "example/app")
	t.Setenv("PLUGIN_CACHE_DIR", "/cache")
	_, err := Load("docker")
	if err == nil || !strings.Contains(err.Error(), "PLUGIN_CACHE_DIR") {
		t.Fatalf("Load() error = %v, want cache directory rejection", err)
	}
}

func TestLoadPreservesBuildKitOnlyInputsForRendererRejection(t *testing.T) {
	t.Setenv("PLUGIN_REPO", "example/app")
	t.Setenv("PLUGIN_ATTESTATION", "max")
	t.Setenv("PLUGIN_SIGN", "true")
	t.Setenv("PLUGIN_COSIGN_KEY", "/secrets/cosign.key")
	t.Setenv("PLUGIN_COSIGN_PASSWORD_ENV", "COSIGN_PASSWORD")

	cfg, err := Load("docker")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Attestation != "max" || !cfg.Sign || cfg.CosignKey == "" || cfg.CosignPasswordEnv != "COSIGN_PASSWORD" {
		t.Fatalf("BuildKit-only inputs were not preserved for renderer validation: %#v", cfg)
	}
}
