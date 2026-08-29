//go:build !windows

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/harness-community/drone-kimia/internal/auth"
	"github.com/harness-community/drone-kimia/internal/config"
	"github.com/harness-community/drone-kimia/internal/destination"
	"github.com/harness-community/drone-kimia/internal/kimia"
)

func TestRunBuildOnlyWritesArtifactAndDroneOutput(t *testing.T) {
	temporary := t.TempDir()
	argumentsPath := filepath.Join(temporary, "arguments")
	fakeKimia := filepath.Join(temporary, "kimia")
	script := `#!/bin/sh
set -eu
: > "$ARGUMENTS_PATH"
for argument in "$@"; do
  printf '%s\n' "$argument" >> "$ARGUMENTS_PATH"
  case "$argument" in
    --digest-file=*) printf '%s\n' 'sha256:abc123' > "${argument#*=}" ;;
  esac
done
`
	if err := os.WriteFile(fakeKimia, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ARGUMENTS_PATH", argumentsPath)
	t.Setenv("KIMIA_EXECUTABLE", fakeKimia)
	t.Setenv("DOCKER_CONFIG", filepath.Join(temporary, "docker"))
	t.Setenv("PLUGIN_REPO", "example/app")
	t.Setenv("PLUGIN_TAG", "test")
	t.Setenv("PLUGIN_NO_PUSH", "true")
	t.Setenv("PLUGIN_ARTIFACT_FILE", filepath.Join(temporary, "results", "artifact.json"))
	t.Setenv("DRONE_OUTPUT", filepath.Join(temporary, "results", "output.env"))
	clearProxyEnvironment(t)

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), "docker", Streams{Stdout: &stdout, Stderr: &stderr})
	if err != nil {
		t.Fatal(err)
	}
	arguments, err := os.ReadFile(argumentsPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"--destination=example/app:test",
		"--no-push",
		"--digest-file=",
	} {
		if !strings.Contains(string(arguments), expected) {
			t.Fatalf("arguments %q do not contain %q", arguments, expected)
		}
	}
	artifact, err := os.ReadFile(filepath.Join(temporary, "results", "artifact.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(artifact), `"image": "example/app:test"`) || !strings.Contains(string(artifact), `"digest": "sha256:abc123"`) {
		t.Fatalf("unexpected artifact: %s", artifact)
	}
	output, err := os.ReadFile(filepath.Join(temporary, "results", "output.env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), "sha256:abc123") {
		t.Fatalf("unexpected DRONE_OUTPUT: %s", output)
	}
}

func TestCloudBuildOnlyKeepsDockerRegistryForBaseImages(t *testing.T) {
	temporary := t.TempDir()
	argumentsPath := filepath.Join(temporary, "arguments")
	fakeKimia := filepath.Join(temporary, "kimia")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$@" > "$ARGUMENTS_PATH"
`
	if err := os.WriteFile(fakeKimia, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ARGUMENTS_PATH", argumentsPath)
	t.Setenv("KIMIA_EXECUTABLE", fakeKimia)
	t.Setenv("DOCKER_CONFIG", filepath.Join(temporary, "docker"))
	t.Setenv("PLUGIN_REPO", "team/app")
	t.Setenv("PLUGIN_TAG", "test")
	t.Setenv("PLUGIN_NO_PUSH", "true")
	t.Setenv("DOCKER_REGISTRY", "base-images.example")
	t.Setenv("DOCKER_USERNAME", "base-user")
	t.Setenv("DOCKER_PASSWORD", "base-password")
	clearProxyEnvironment(t)

	if err := Run(context.Background(), "ecr", Streams{}); err != nil {
		t.Fatal(err)
	}
	arguments, err := os.ReadFile(argumentsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(arguments), "--destination=team/app:test") {
		t.Fatalf("base-image registry leaked into destination arguments: %s", arguments)
	}
	if strings.Contains(string(arguments), "base-images.example/team/app") {
		t.Fatalf("base-image registry leaked into destination arguments: %s", arguments)
	}
}

func TestDirectDestinationSelectsAuthenticationHostAndUsesPrivateConfig(t *testing.T) {
	temporary := t.TempDir()
	configCapture := filepath.Join(temporary, "config-capture.json")
	environmentCapture := filepath.Join(temporary, "environment-capture")
	fakeKimia := filepath.Join(temporary, "kimia")
	script := `#!/bin/sh
set -eu
cp "$DOCKER_CONFIG/config.json" "$CONFIG_CAPTURE"
env > "$ENVIRONMENT_CAPTURE"
`
	if err := os.WriteFile(fakeKimia, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	sourceConfigDir := filepath.Join(temporary, "source-docker")
	if err := os.MkdirAll(sourceConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}
	sourceConfig := []byte(`{"auths":{"source.example":{"auth":"c291cmNlOnZhbHVl"}},"experimental":"enabled"}`)
	if err := os.WriteFile(filepath.Join(sourceConfigDir, "config.json"), sourceConfig, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CONFIG_CAPTURE", configCapture)
	t.Setenv("ENVIRONMENT_CAPTURE", environmentCapture)
	t.Setenv("KIMIA_EXECUTABLE", fakeKimia)
	t.Setenv("DOCKER_CONFIG", sourceConfigDir)
	t.Setenv("PLUGIN_DESTINATIONS", "registry.example/team/app:test")
	t.Setenv("PLUGIN_NO_PUSH", "true")
	t.Setenv("PLUGIN_USERNAME", "push-user")
	t.Setenv("PLUGIN_PASSWORD", "push-password")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "must-not-reach-kimia")
	clearProxyEnvironment(t)

	if err := Run(context.Background(), "docker", Streams{}); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("DOCKER_CONFIG"); got != sourceConfigDir {
		t.Fatalf("DOCKER_CONFIG was not restored: %q", got)
	}
	unchanged, err := os.ReadFile(filepath.Join(sourceConfigDir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged) != string(sourceConfig) {
		t.Fatalf("source Docker config was modified: %s", unchanged)
	}

	captured, err := os.ReadFile(configCapture)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Auths map[string]json.RawMessage `json:"auths"`
	}
	if err := json.Unmarshal(captured, &document); err != nil {
		t.Fatal(err)
	}
	if _, ok := document.Auths["registry.example"]; !ok {
		t.Fatalf("destination credential was not stored under registry.example: %s", captured)
	}
	environment, err := os.ReadFile(environmentCapture)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"PLUGIN_PASSWORD=", "PLUGIN_USERNAME=", "AWS_SECRET_ACCESS_KEY="} {
		if strings.Contains(string(environment), secret) {
			t.Fatalf("Kimia child inherited connector secret %q", secret)
		}
	}
}

func TestRunRejectsUnqualifiedNativeDestination(t *testing.T) {
	t.Setenv("PLUGIN_DESTINATIONS", "team/app:test")
	t.Setenv("PLUGIN_NO_PUSH", "true")
	clearProxyEnvironment(t)
	err := Run(context.Background(), "docker", Streams{})
	if err == nil || !strings.Contains(err.Error(), "explicit registry host") {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRegistryCacheRequiresAuthenticationInBuildOnlyMode(t *testing.T) {
	t.Parallel()
	if !requiresRegistryAuthentication(config.Config{
		Provider:    string(auth.ProviderECR),
		NoPush:      true,
		ImportCache: []string{"type=registry,ref=123456789012.dkr.ecr.us-east-1.amazonaws.com/cache"},
	}, "123456789012.dkr.ecr.us-east-1.amazonaws.com", true) {
		t.Fatal("registry cache did not request provider authentication")
	}
	if requiresRegistryAuthentication(config.Config{
		Provider:    string(auth.ProviderECR),
		NoPush:      true,
		ImportCache: []string{"type=local,src=/cache"},
	}, "", false) {
		t.Fatal("local-only cache unexpectedly requested provider authentication")
	}
}

func TestGARBuildOnlyRegistryCachePreparesAuthentication(t *testing.T) {
	temporary := t.TempDir()
	argumentsPath := filepath.Join(temporary, "arguments")
	configPath := filepath.Join(temporary, "config.json")
	fakeKimia := filepath.Join(temporary, "kimia")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$@" > "$ARGUMENTS_PATH"
cp "$DOCKER_CONFIG/config.json" "$CONFIG_PATH"
`
	if err := os.WriteFile(fakeKimia, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARGUMENTS_PATH", argumentsPath)
	t.Setenv("CONFIG_PATH", configPath)
	t.Setenv("KIMIA_EXECUTABLE", fakeKimia)
	t.Setenv("PLUGIN_REPO", "us-central1-docker.pkg.dev/project/app")
	t.Setenv("PLUGIN_TAG", "test")
	t.Setenv("PLUGIN_NO_PUSH", "true")
	t.Setenv("PLUGIN_CACHE_REPO", "project/cache")
	t.Setenv("PLUGIN_JSON_KEY", `{"type":"service_account","private_key":"secret"}`)
	clearProxyEnvironment(t)

	if err := Run(context.Background(), "gar", Streams{}); err != nil {
		t.Fatal(err)
	}
	arguments, err := os.ReadFile(argumentsPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"--no-push",
		"--import-cache=type=registry,ref=us-central1-docker.pkg.dev/project/cache",
		"--export-cache=type=registry,ref=us-central1-docker.pkg.dev/project/cache,mode=max",
	} {
		if !strings.Contains(string(arguments), expected) {
			t.Fatalf("arguments %q do not contain %q", arguments, expected)
		}
	}
	dockerConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dockerConfig), "us-central1-docker.pkg.dev") {
		t.Fatalf("GAR credential missing from Docker config: %s", dockerConfig)
	}
}

func TestCacheOnlyAuthenticationDoesNotRewriteImageDestination(t *testing.T) {
	temporary := t.TempDir()
	argumentsPath := filepath.Join(temporary, "arguments")
	configPath := filepath.Join(temporary, "config.json")
	fakeKimia := filepath.Join(temporary, "kimia")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$@" > "$ARGUMENTS_PATH"
cp "$DOCKER_CONFIG/config.json" "$CONFIG_PATH"
`
	if err := os.WriteFile(fakeKimia, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ARGUMENTS_PATH", argumentsPath)
	t.Setenv("CONFIG_PATH", configPath)
	t.Setenv("KIMIA_EXECUTABLE", fakeKimia)
	t.Setenv("PLUGIN_REPO", "local/check")
	t.Setenv("PLUGIN_TAG", "test")
	t.Setenv("PLUGIN_NO_PUSH", "true")
	t.Setenv("PLUGIN_CACHE_REPO", "cache.example/team/cache")
	t.Setenv("PLUGIN_USERNAME", "cache-user")
	t.Setenv("PLUGIN_PASSWORD", "cache-password")
	clearProxyEnvironment(t)

	if err := Run(context.Background(), "docker", Streams{}); err != nil {
		t.Fatal(err)
	}
	arguments, err := os.ReadFile(argumentsPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(arguments), "--destination=local/check:test") {
		t.Fatalf("cache registry rewrote build-only destination: %s", arguments)
	}
	if !strings.Contains(string(arguments), "ref=cache.example/team/cache") {
		t.Fatalf("cache repository missing from Kimia arguments: %s", arguments)
	}
	dockerConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dockerConfig), "cache.example") {
		t.Fatalf("cache credential used the wrong host: %s", dockerConfig)
	}
}

func TestDroneOutputKeepsTarPathWhenDigestIsMissing(t *testing.T) {
	temporary := t.TempDir()
	tarPath := filepath.Join(temporary, "image.tar")
	if err := os.WriteFile(tarPath, []byte("tar"), 0o600); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(temporary, "output.env")
	var stderr bytes.Buffer
	writeRequestedResults(config.Config{
		Provider:    string(auth.ProviderDocker),
		DigestFile:  filepath.Join(temporary, "missing-digest"),
		DroneOutput: outputPath,
		TarPath:     tarPath,
	}, destination.Result{}, "", &stderr)
	output, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), "IMAGE_TAR_PATH") {
		t.Fatalf("DRONE_OUTPUT did not preserve tar path: %s", output)
	}
}

func TestDockerHubArtifactUsesLegacyRegistryURL(t *testing.T) {
	temporary := t.TempDir()
	digestPath := filepath.Join(temporary, "digest")
	if err := os.WriteFile(digestPath, []byte("sha256:abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	artifactPath := filepath.Join(temporary, "artifact.json")
	writeRequestedResults(config.Config{
		Provider:     string(auth.ProviderDocker),
		DigestFile:   digestPath,
		ArtifactFile: artifactPath,
	}, destination.Result{Destinations: []string{"docker.io/team/app:test"}}, "docker.io", nil)
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(artifact), `"registryUrl": "https://index.docker.io/v1/"`) {
		t.Fatalf("Docker Hub artifact registry URL changed: %s", artifact)
	}
}

func TestKimiaEnvironmentKeepsOnlyExplicitCosignSecret(t *testing.T) {
	t.Setenv("PLUGIN_PASSWORD", "plugin-secret")
	t.Setenv("AWS_SESSION_TOKEN", "aws-secret")
	t.Setenv("AZURE_CLIENT_SECRET", "azure-secret")
	t.Setenv("DOCKER_CONFIG", "/tmp/private-docker-config")
	t.Setenv("COSIGN_PASSWORD", "cosign-secret")
	environment := strings.Join(kimiaEnvironment("COSIGN_PASSWORD"), "\n")
	for _, absent := range []string{"PLUGIN_PASSWORD=", "AWS_SESSION_TOKEN=", "AZURE_CLIENT_SECRET="} {
		if strings.Contains(environment, absent) {
			t.Fatalf("Kimia environment contains %s", absent)
		}
	}
	for _, present := range []string{"DOCKER_CONFIG=/tmp/private-docker-config", "COSIGN_PASSWORD=cosign-secret"} {
		if !strings.Contains(environment, present) {
			t.Fatalf("Kimia environment does not contain %s", present)
		}
	}
}

func TestExecuteCancellationTerminatesKimiaProcessGroup(t *testing.T) {
	temporary := t.TempDir()
	childPIDPath := filepath.Join(temporary, "child-pid")
	scriptPath := filepath.Join(temporary, "kimia")
	script := `#!/bin/sh
set -eu
trap 'exit 0' TERM INT
sh -c 'trap "" TERM; while :; do sleep 1; done' &
child=$!
printf '%s\n' "$child" > "$CHILD_PID_PATH"
wait "$child"
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHILD_PID_PATH", childPIDPath)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(childPIDPath); err == nil {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
	}()

	started := time.Now()
	err := execute(ctx, kimia.Command{Path: scriptPath}, Streams{}, "")
	if err == nil || !strings.Contains(err.Error(), "interrupted") {
		t.Fatalf("execute() error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("process group cancellation took %s", elapsed)
	}
	pidData, err := os.ReadFile(childPIDPath)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidData)))
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		if err := syscall.Kill(pid, 0); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Kimia descendant process %d is still running", pid)
		}
		time.Sleep(10 * time.Millisecond)
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
