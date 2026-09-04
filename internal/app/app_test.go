//go:build !windows

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/harness-community/drone-kimia/internal/archivepush"
	"github.com/harness-community/drone-kimia/internal/auth"
	"github.com/harness-community/drone-kimia/internal/config"
	"github.com/harness-community/drone-kimia/internal/destination"
	"github.com/harness-community/drone-kimia/internal/kimia"
	"github.com/harness-community/drone-kimia/internal/registrydigest"
	"github.com/harness-community/drone-kimia/internal/result"
)

func TestRunBuildOnlyWritesArtifactAndDroneOutput(t *testing.T) {
	rejectRegistryDigestResolution(t)
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

func TestRunBuildOnlyCanonicalizesBuildahDigestFiles(t *testing.T) {
	rejectRegistryDigestResolution(t)
	temporary := t.TempDir()
	fakeKimia := filepath.Join(temporary, "kimia")
	imageID := strings.Repeat("A1", 32)
	script := `#!/bin/sh
set -eu
for argument in "$@"; do
  case "$argument" in
    --digest-file=*) printf '%s\n' "$IMAGE_ID" > "${argument#*=}" ;;
  esac
done
`
	if err := os.WriteFile(fakeKimia, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	digestPath := filepath.Join(temporary, "digest")
	imageNamePath := filepath.Join(temporary, "image-name")
	t.Setenv("IMAGE_ID", imageID)
	t.Setenv("KIMIA_EXECUTABLE", fakeKimia)
	t.Setenv("DOCKER_CONFIG", filepath.Join(temporary, "docker"))
	t.Setenv("PLUGIN_REPO", "registry.example/team/app")
	t.Setenv("PLUGIN_TAG", "test")
	t.Setenv("PLUGIN_NO_PUSH", "true")
	t.Setenv("PLUGIN_DIGEST_FILE", digestPath)
	t.Setenv("PLUGIN_IMAGE_NAME_WITH_DIGEST_FILE", imageNamePath)
	clearProxyEnvironment(t)

	if err := Run(context.Background(), "docker", Streams{}); err != nil {
		t.Fatal(err)
	}
	wantDigest := "sha256:" + strings.ToLower(imageID)
	digestData, err := os.ReadFile(digestPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(digestData)); got != wantDigest {
		t.Fatalf("digest = %q, want %q", got, wantDigest)
	}
	imageNameData, err := os.ReadFile(imageNamePath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(imageNameData), "registry.example/team/app@"+wantDigest; got != want {
		t.Fatalf("image name with digest = %q, want %q", got, want)
	}
}

func TestRunPushOnlyUsesArchivePublisherAndWritesResults(t *testing.T) {
	rejectRegistryDigestResolution(t)
	temporary := t.TempDir()
	sourceTar := filepath.Join(temporary, "imageci.tar")
	if err := os.WriteFile(sourceTar, []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}

	previousPush := pushImageArchive
	t.Cleanup(func() { pushImageArchive = previousPush })
	var received archivepush.Options
	pushImageArchive = func(ctx context.Context, options archivepush.Options) (string, error) {
		received = options
		return "sha256:pushed", nil
	}

	artifactPath := filepath.Join(temporary, "artifact.json")
	outputPath := filepath.Join(temporary, "output.env")
	t.Setenv("DOCKER_CONFIG", filepath.Join(temporary, "docker"))
	t.Setenv("PLUGIN_REPO", "registry.example/team/app")
	t.Setenv("PLUGIN_TAGS", "test,latest")
	t.Setenv("PLUGIN_PUSH_ONLY", "true")
	t.Setenv("PLUGIN_SOURCE_TAR_PATH", sourceTar)
	t.Setenv("PLUGIN_ARTIFACT_FILE", artifactPath)
	t.Setenv("DRONE_OUTPUT", outputPath)
	t.Setenv("PLUGIN_SNAPSHOT_MODE", "redo")
	clearProxyEnvironment(t)

	if err := Run(context.Background(), "docker", Streams{}); err != nil {
		t.Fatal(err)
	}
	if received.SourceTarPath != sourceTar {
		t.Fatalf("source tar = %q", received.SourceTarPath)
	}
	wantDestinations := []string{"registry.example/team/app:test", "registry.example/team/app:latest"}
	if strings.Join(received.Destinations, ",") != strings.Join(wantDestinations, ",") {
		t.Fatalf("destinations = %#v, want %#v", received.Destinations, wantDestinations)
	}

	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`"image": "registry.example/team/app:test"`, `"digest": "sha256:pushed"`} {
		if !strings.Contains(string(artifact), expected) {
			t.Fatalf("artifact %q does not contain %q", artifact, expected)
		}
	}
	output, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), "sha256:pushed") || strings.Contains(string(output), "IMAGE_TAR_PATH") {
		t.Fatalf("unexpected push-only output: %s", output)
	}
}

func TestRunNormalPushUsesVerifiedManifestDigestsForEveryOutput(t *testing.T) {
	temporary := t.TempDir()
	argumentsPath := filepath.Join(temporary, "arguments")
	fakeKimia := filepath.Join(temporary, "kimia")
	configDigest := "sha256:" + strings.Repeat("c", 64)
	script := `#!/bin/sh
set -eu
printf '%s\n' "$@" > "$ARGUMENTS_PATH"
for argument in "$@"; do
  case "$argument" in
    --digest-file=*) printf '%s\n' "$CONFIG_DIGEST" > "${argument#*=}" ;;
    --image-name-with-digest-file=*) printf '%s\n' "config@$CONFIG_DIGEST" > "${argument#*=}" ;;
  esac
done
`
	if err := os.WriteFile(fakeKimia, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	firstDestination := "registry.example/team/app:first"
	secondDestination := "registry.example/team/app:second"
	firstDigest := "sha256:" + strings.Repeat("1", 64)
	secondDigest := "sha256:" + strings.Repeat("2", 64)
	digestPath := filepath.Join(temporary, "digest")
	imageNamePath := filepath.Join(temporary, "image-name")
	artifactPath := filepath.Join(temporary, "artifact.json")
	outputPath := filepath.Join(temporary, "output.env")
	sourceConfigDir := filepath.Join(temporary, "source-docker-config")
	if err := os.MkdirAll(sourceConfigDir, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("ARGUMENTS_PATH", argumentsPath)
	t.Setenv("CONFIG_DIGEST", configDigest)
	t.Setenv("KIMIA_EXECUTABLE", fakeKimia)
	t.Setenv("DOCKER_CONFIG", sourceConfigDir)
	t.Setenv("PLUGIN_DESTINATIONS", firstDestination+";"+secondDestination)
	t.Setenv("PLUGIN_INSECURE", "true")
	t.Setenv("PLUGIN_INSECURE_REGISTRY", "registry.example,cache.example")
	t.Setenv("PLUGIN_DIGEST_FILE", digestPath)
	t.Setenv("PLUGIN_IMAGE_NAME_WITH_DIGEST_FILE", imageNamePath)
	t.Setenv("PLUGIN_ARTIFACT_FILE", artifactPath)
	t.Setenv("DRONE_OUTPUT", outputPath)
	clearProxyEnvironment(t)

	previousResolve := resolveRegistryDigests
	t.Cleanup(func() { resolveRegistryDigests = previousResolve })
	var received registrydigest.Options
	resolveRegistryDigests = func(_ context.Context, options registrydigest.Options) (map[string]string, error) {
		received = options
		privateConfigDir := os.Getenv("DOCKER_CONFIG")
		if privateConfigDir == "" || privateConfigDir == sourceConfigDir {
			t.Errorf("resolver DOCKER_CONFIG = %q, want private prepared config", privateConfigDir)
		}
		if _, err := os.Stat(filepath.Join(privateConfigDir, "config.json")); err != nil {
			t.Errorf("resolver cannot read prepared Docker config: %v", err)
		}
		return map[string]string{
			firstDestination:  firstDigest,
			secondDestination: secondDigest,
		}, nil
	}

	if err := Run(context.Background(), "docker", Streams{}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(received.Destinations, ",") != firstDestination+","+secondDestination {
		t.Fatalf("resolver destinations = %#v", received.Destinations)
	}
	if !received.Insecure || strings.Join(received.InsecureRegistries, ",") != "registry.example,cache.example" {
		t.Fatalf("resolver insecure options = %#v", received)
	}
	arguments, err := os.ReadFile(argumentsPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"--digest-file=", "--image-name-with-digest-file="} {
		if strings.Contains(string(arguments), forbidden) {
			t.Fatalf("Kimia received untrusted digest output flag %q: %s", forbidden, arguments)
		}
	}
	digestData, err := os.ReadFile(digestPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(digestData)); got != firstDigest || got == configDigest {
		t.Fatalf("digest output = %q, want first manifest digest %q", got, firstDigest)
	}
	imageNameData, err := os.ReadFile(imageNamePath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(imageNameData), "registry.example/team/app@"+firstDigest; got != want {
		t.Fatalf("image-name output = %q, want %q", got, want)
	}
	artifactData, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	var artifact result.Artifact
	if err := json.Unmarshal(artifactData, &artifact); err != nil {
		t.Fatal(err)
	}
	wantImages := []result.Image{
		{Image: firstDestination, Digest: firstDigest},
		{Image: secondDestination, Digest: secondDigest},
	}
	if len(artifact.Data.Images) != len(wantImages) {
		t.Fatalf("artifact images = %#v, want %#v", artifact.Data.Images, wantImages)
	}
	for index := range wantImages {
		if artifact.Data.Images[index] != wantImages[index] {
			t.Fatalf("artifact image %d = %#v, want %#v", index, artifact.Data.Images[index], wantImages[index])
		}
	}
	output, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), firstDigest) || strings.Contains(string(output), secondDigest) || strings.Contains(string(output), configDigest) {
		t.Fatalf("DRONE_OUTPUT did not use the first verified manifest digest: %s", output)
	}
}

func TestRunNormalPushWarnsAndSuppressesOutputsWhenVerificationFails(t *testing.T) {
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
	digestPath := filepath.Join(temporary, "digest")
	imageNamePath := filepath.Join(temporary, "image-name")
	artifactPath := filepath.Join(temporary, "artifact.json")
	outputPath := filepath.Join(temporary, "output.env")
	for path, contents := range map[string]string{
		digestPath:    "sha256:stale-config",
		imageNamePath: "stale@sha256:config",
		artifactPath:  `{"digest":"stale-config"}`,
		outputPath:    `digest="stale-config"`,
	} {
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("ARGUMENTS_PATH", argumentsPath)
	t.Setenv("KIMIA_EXECUTABLE", fakeKimia)
	t.Setenv("DOCKER_CONFIG", filepath.Join(temporary, "docker"))
	t.Setenv("PLUGIN_DESTINATIONS", "registry.example/team/app:test")
	t.Setenv("PLUGIN_DIGEST_FILE", digestPath)
	t.Setenv("PLUGIN_IMAGE_NAME_WITH_DIGEST_FILE", imageNamePath)
	t.Setenv("PLUGIN_ARTIFACT_FILE", artifactPath)
	t.Setenv("DRONE_OUTPUT", outputPath)
	clearProxyEnvironment(t)

	previousResolve := resolveRegistryDigests
	t.Cleanup(func() { resolveRegistryDigests = previousResolve })
	resolveRegistryDigests = func(context.Context, registrydigest.Options) (map[string]string, error) {
		return nil, errors.New("registry temporarily unavailable")
	}

	var stderr bytes.Buffer
	if err := Run(context.Background(), "docker", Streams{Stderr: &stderr}); err != nil {
		t.Fatalf("successful push became a plugin failure: %v", err)
	}
	for _, expected := range []string{"image push succeeded", "manifest digest verification failed", "registry temporarily unavailable", "suppressed"} {
		if !strings.Contains(stderr.String(), expected) {
			t.Fatalf("warning %q does not contain %q", stderr.String(), expected)
		}
	}
	for _, path := range []string{digestPath, imageNamePath, artifactPath, outputPath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("unverified output %q was not suppressed: %v", path, err)
		}
	}
	arguments, err := os.ReadFile(argumentsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(arguments), "--digest-file=") || strings.Contains(string(arguments), "--image-name-with-digest-file=") {
		t.Fatalf("Kimia received digest output paths despite remote verification: %s", arguments)
	}
}

func TestRunNormalPushWithoutDigestOutputsSkipsRegistryLookup(t *testing.T) {
	temporary := t.TempDir()
	fakeKimia := filepath.Join(temporary, "kimia")
	if err := os.WriteFile(fakeKimia, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KIMIA_EXECUTABLE", fakeKimia)
	t.Setenv("DOCKER_CONFIG", filepath.Join(temporary, "docker"))
	t.Setenv("PLUGIN_DESTINATIONS", "registry.example/team/app:test")
	// Harness Run steps can inject DRONE_OUTPUT into the test process. Clear
	// every result path so this test exercises the normal-push/no-output path
	// independently of its CI environment.
	for _, key := range []string{
		"PLUGIN_DIGEST_FILE",
		"PLUGIN_IMAGE_NAME_WITH_DIGEST_FILE",
		"PLUGIN_ARTIFACT_FILE",
		"DRONE_OUTPUT",
	} {
		t.Setenv(key, "")
	}
	clearProxyEnvironment(t)
	rejectRegistryDigestResolution(t)

	if err := Run(context.Background(), "docker", Streams{}); err != nil {
		t.Fatal(err)
	}
}

func TestRunGARPreservesHarnessProjectNamespace(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "harness")
	home := filepath.Join(root, "home", "kimia")
	for _, directory := range []string{workspace, home} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(workspace, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)

	argumentsPath := filepath.Join(root, "arguments")
	configPath := filepath.Join(root, "config.json")
	fakeKimia := filepath.Join(root, "kimia")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$@" > "$ARGUMENTS_PATH"
cp "$DOCKER_CONFIG/config.json" "$CONFIG_PATH"
for argument in "$@"; do
  case "$argument" in
    --digest-file=*) printf '%s\n' 'sha256:gar-build' > "${argument#*=}" ;;
  esac
done
`
	if err := os.WriteFile(fakeKimia, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	artifactPath := filepath.Join(root, "artifact.json")
	t.Setenv("HOME", home)
	t.Setenv("HARNESS_WORKSPACE", workspace)
	t.Setenv("DRONE_WORKSPACE", workspace)
	t.Setenv("ARGUMENTS_PATH", argumentsPath)
	t.Setenv("CONFIG_PATH", configPath)
	t.Setenv("KIMIA_EXECUTABLE", fakeKimia)
	t.Setenv("DOCKER_CONFIG", filepath.Join(root, "docker"))
	t.Setenv("PLUGIN_REGISTRY", "us-central1-docker.pkg.dev/example-project")
	t.Setenv("PLUGIN_REPO", "sample-app")
	t.Setenv("PLUGIN_TAGS", "test")
	t.Setenv("PLUGIN_CACHE_REPO", "cache")
	t.Setenv("PLUGIN_JSON_KEY", `{"type":"service_account","private_key":"secret"}`)
	t.Setenv("PLUGIN_ARTIFACT_FILE", artifactPath)
	t.Setenv("PLUGIN_SNAPSHOT_MODE", "redo")
	t.Setenv("PLUGIN_METADATA_FILE", "/addon/tmp/buildx-metadata.json")
	clearProxyEnvironment(t)
	previousResolve := resolveRegistryDigests
	t.Cleanup(func() { resolveRegistryDigests = previousResolve })
	resolveRegistryDigests = func(_ context.Context, options registrydigest.Options) (map[string]string, error) {
		return map[string]string{
			options.Destinations[0]: "sha256:gar-manifest",
		}, nil
	}

	if err := Run(context.Background(), "gar", Streams{}); err != nil {
		t.Fatal(err)
	}
	arguments, err := os.ReadFile(argumentsPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"--destination=us-central1-docker.pkg.dev/example-project/sample-app:test",
		"--cache=true",
		"--buildah-opt=--cache-from us-central1-docker.pkg.dev/example-project/cache",
		"--buildah-opt=--cache-to us-central1-docker.pkg.dev/example-project/cache",
	} {
		if !strings.Contains(string(arguments), expected) {
			t.Fatalf("arguments %q do not contain %q", arguments, expected)
		}
	}
	assertDockerConfigUsesRegistryHost(t, configPath, "us-central1-docker.pkg.dev")
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`"registryUrl": "us-central1-docker.pkg.dev/example-project"`,
		`"image": "us-central1-docker.pkg.dev/example-project/sample-app:test"`,
		`"digest": "sha256:gar-manifest"`,
	} {
		if !strings.Contains(string(artifact), expected) {
			t.Fatalf("artifact %q does not contain %q", artifact, expected)
		}
	}
}

func TestRunGARPushOnlyPreservesHarnessProjectNamespace(t *testing.T) {
	root := t.TempDir()
	sourceTar := filepath.Join(root, "imageci.tar")
	if err := os.WriteFile(sourceTar, []byte("archive"), 0o600); err != nil {
		t.Fatal(err)
	}

	previousPush := pushImageArchive
	t.Cleanup(func() { pushImageArchive = previousPush })
	var received archivepush.Options
	configPath := filepath.Join(root, "push-config.json")
	pushImageArchive = func(ctx context.Context, options archivepush.Options) (string, error) {
		received = options
		configDir := os.Getenv("DOCKER_CONFIG")
		data, err := os.ReadFile(filepath.Join(configDir, "config.json"))
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(configPath, data, 0o600); err != nil {
			return "", err
		}
		return "sha256:gar-pushed", nil
	}

	artifactPath := filepath.Join(root, "artifact.json")
	t.Setenv("DOCKER_CONFIG", filepath.Join(root, "docker"))
	t.Setenv("PLUGIN_REGISTRY", "us-central1-docker.pkg.dev/example-project")
	t.Setenv("PLUGIN_REPO", "sample-app")
	t.Setenv("PLUGIN_TAGS", "test")
	t.Setenv("PLUGIN_PUSH_ONLY", "true")
	t.Setenv("PLUGIN_SOURCE_TAR_PATH", sourceTar)
	t.Setenv("PLUGIN_JSON_KEY", `{"type":"service_account","private_key":"secret"}`)
	t.Setenv("PLUGIN_ARTIFACT_FILE", artifactPath)
	clearProxyEnvironment(t)

	if err := Run(context.Background(), "gar", Streams{}); err != nil {
		t.Fatal(err)
	}
	wantDestination := "us-central1-docker.pkg.dev/example-project/sample-app:test"
	if len(received.Destinations) != 1 || received.Destinations[0] != wantDestination {
		t.Fatalf("push-only destinations = %#v, want %q", received.Destinations, wantDestination)
	}
	assertDockerConfigUsesRegistryHost(t, configPath, "us-central1-docker.pkg.dev")
	artifact, err := os.ReadFile(artifactPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`"registryUrl": "us-central1-docker.pkg.dev/example-project"`,
		`"image": "us-central1-docker.pkg.dev/example-project/sample-app:test"`,
		`"digest": "sha256:gar-pushed"`,
	} {
		if !strings.Contains(string(artifact), expected) {
			t.Fatalf("artifact %q does not contain %q", artifact, expected)
		}
	}
}

func assertDockerConfigUsesRegistryHost(t *testing.T, path, registryHost string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Auths map[string]json.RawMessage `json:"auths"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if _, ok := document.Auths[registryHost]; !ok {
		t.Fatalf("Docker config does not contain host-only credential %q: %s", registryHost, data)
	}
	for key := range document.Auths {
		if strings.HasPrefix(key, registryHost+"/") {
			t.Fatalf("Docker config credential includes repository namespace %q: %s", key, data)
		}
	}
}

func TestRunAdaptsHarnessWorkspaceAndPublishesRelativeTar(t *testing.T) {
	rejectRegistryDigestResolution(t)
	root := t.TempDir()
	home := filepath.Join(root, "home", "kimia")
	workspace := filepath.Join(root, "harness")
	for _, directory := range []string{home, workspace} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(workspace, "Dockerfile"), []byte("FROM scratch\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)

	contextCapture := filepath.Join(root, "context")
	fakeKimia := filepath.Join(root, "kimia")
	script := `#!/bin/sh
set -eu
context=
tar_path=
for argument in "$@"; do
  case "$argument" in
    --context=*) context=${argument#*=} ;;
    --tar-path=*) tar_path=${argument#*=} ;;
    --digest-file=*) printf '%s\n' 'sha256:harness' > "${argument#*=}" ;;
  esac
done
test -f "$context/Dockerfile"
printf '%s\n' "$context" > "$CONTEXT_CAPTURE"
mkdir -p "$(dirname "$tar_path")"
printf '%s' 'harness archive' > "$tar_path"
`
	if err := os.WriteFile(fakeKimia, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	outputPath := filepath.Join(workspace, "drone.env")
	t.Setenv("HOME", home)
	t.Setenv("HARNESS_WORKSPACE", workspace)
	t.Setenv("DRONE_WORKSPACE", workspace)
	t.Setenv("CONTEXT_CAPTURE", contextCapture)
	t.Setenv("KIMIA_EXECUTABLE", fakeKimia)
	t.Setenv("DOCKER_CONFIG", filepath.Join(root, "docker"))
	t.Setenv("PLUGIN_REPO", "example.invalid/team/app")
	t.Setenv("PLUGIN_TAG", "test")
	t.Setenv("PLUGIN_NO_PUSH", "true")
	t.Setenv("PLUGIN_DESTINATION_TAR_PATH", "imageci.tar")
	t.Setenv("PLUGIN_SNAPSHOT_MODE", "redo")
	t.Setenv("PLUGIN_METADATA_FILE", "/addon/tmp/buildx-metadata.json")
	t.Setenv("DRONE_OUTPUT", outputPath)
	clearProxyEnvironment(t)

	if err := Run(context.Background(), "docker", Streams{}); err != nil {
		t.Fatal(err)
	}
	archive, err := os.ReadFile(filepath.Join(workspace, "imageci.tar"))
	if err != nil {
		t.Fatal(err)
	}
	if string(archive) != "harness archive" {
		t.Fatalf("archive = %q", archive)
	}
	proxiedContext, err := os.ReadFile(contextCapture)
	if err != nil {
		t.Fatal(err)
	}
	proxy := strings.TrimSpace(string(proxiedContext))
	if !strings.HasPrefix(proxy, home+string(filepath.Separator)) {
		t.Fatalf("Kimia context = %q, want private path under %q", proxy, home)
	}
	if _, err := os.Lstat(proxy); !os.IsNotExist(err) {
		t.Fatalf("workspace proxy was not cleaned: %v", err)
	}
	output, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), `IMAGE_TAR_PATH="imageci.tar"`) {
		t.Fatalf("DRONE_OUTPUT did not preserve relative tar path: %s", output)
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

func TestShouldResolveRegistryDigestsOnlyForNormalPushOutputs(t *testing.T) {
	for _, test := range []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{name: "normal push digest file", cfg: config.Config{DigestFile: "/digest"}, want: true},
		{name: "normal push image name", cfg: config.Config{ImageNameWithDigestFile: "/image"}, want: true},
		{name: "normal push artifact", cfg: config.Config{ArtifactFile: "/artifact"}, want: true},
		{name: "normal push Drone output", cfg: config.Config{DroneOutput: "/output"}, want: true},
		{name: "normal push without output", cfg: config.Config{}, want: false},
		{name: "build only", cfg: config.Config{NoPush: true, DigestFile: "/digest"}, want: false},
		{name: "tar export", cfg: config.Config{TarPath: "/image.tar", DigestFile: "/digest"}, want: false},
		{name: "push only", cfg: config.Config{PushOnly: true, DigestFile: "/digest"}, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldResolveRegistryDigests(test.cfg); got != test.want {
				t.Fatalf("shouldResolveRegistryDigests() = %t, want %t", got, test.want)
			}
		})
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
		"--cache=true",
		"--buildah-opt=--cache-from us-central1-docker.pkg.dev/project/cache",
		"--buildah-opt=--cache-to us-central1-docker.pkg.dev/project/cache",
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
	if !strings.Contains(string(arguments), "--buildah-opt=--cache-from cache.example/team/cache") {
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
		if !processIsRunning(pid) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Kimia descendant process %d is still running", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func processIsRunning(pid int) bool {
	// kill(pid, 0) reports a zombie as an existing process. In a container
	// whose PID 1 does not reap adopted grandchildren, the descendant killed
	// above can remain a zombie until the container exits even though it is no
	// longer running. Use Linux's process state when available and retain the
	// portable signal probe as a fallback for other Unix systems.
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err == nil {
		if closingParen := bytes.LastIndexByte(stat, ')'); closingParen >= 0 {
			fields := bytes.Fields(stat[closingParen+1:])
			if len(fields) > 0 && (bytes.Equal(fields[0], []byte("Z")) || bytes.Equal(fields[0], []byte("X"))) {
				return false
			}
		}
	}
	return syscall.Kill(pid, 0) == nil
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

func rejectRegistryDigestResolution(t *testing.T) {
	t.Helper()
	previousResolve := resolveRegistryDigests
	t.Cleanup(func() { resolveRegistryDigests = previousResolve })
	resolveRegistryDigests = func(context.Context, registrydigest.Options) (map[string]string, error) {
		t.Fatal("registry digest resolver was called for an excluded operation")
		return nil, nil
	}
}
