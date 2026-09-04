package result

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWriteArtifactUsesResolvedDestinations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "artifact.json")
	destinations := []string{"registry.example/team/app:1", "registry.example/team/app:latest"}
	if err := WriteArtifact(path, "GAR", "registry.example", "sha256:abc", destinations); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var artifact Artifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatal(err)
	}
	if artifact.Kind != "docker/v1" || artifact.Data.RegistryType != "GAR" {
		t.Fatalf("unexpected artifact: %#v", artifact)
	}
	got := []string{artifact.Data.Images[0].Image, artifact.Data.Images[1].Image}
	if !reflect.DeepEqual(got, destinations) {
		t.Fatalf("images = %#v, want %#v", got, destinations)
	}
}

func TestWriteArtifactUsesPerDestinationDigests(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact.json")
	destinations := []string{"registry.example/team/app:one", "registry.example/team/app:two"}
	digests := map[string]string{
		destinations[0]: "sha256:first",
		destinations[1]: "sha256:second",
	}
	if err := WriteArtifactWithDigests(path, "Docker", "registry.example", destinations, digests); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var artifact Artifact
	if err := json.Unmarshal(data, &artifact); err != nil {
		t.Fatal(err)
	}
	want := []Image{
		{Image: destinations[0], Digest: "sha256:first"},
		{Image: destinations[1], Digest: "sha256:second"},
	}
	if !reflect.DeepEqual(artifact.Data.Images, want) {
		t.Fatalf("images = %#v, want %#v", artifact.Data.Images, want)
	}
}

func TestWriteArtifactRejectsMissingDestinationDigest(t *testing.T) {
	err := WriteArtifactWithDigests(
		filepath.Join(t.TempDir(), "artifact.json"),
		"Docker",
		"registry.example",
		[]string{"registry.example/team/app:test"},
		map[string]string{},
	)
	if err == nil || !strings.Contains(err.Error(), "registry.example/team/app:test") {
		t.Fatalf("WriteArtifactWithDigests() error = %v", err)
	}
}

func TestWriteDroneOutput(t *testing.T) {
	dir := t.TempDir()
	tarPath := filepath.Join(dir, "image.tar")
	if err := os.WriteFile(tarPath, []byte("tar"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "nested", "output.env")
	if err := WriteDroneOutput(path, "sha256:abc", tarPath); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "IMAGE_TAR_PATH=\"" + tarPath + "\"\ndigest=\"sha256:abc\"\n"
	if string(data) != want {
		t.Fatalf("output = %q, want %q", data, want)
	}
}

func TestReadDigestTrimsNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "digest")
	if err := os.WriteFile(path, []byte("sha256:abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "sha256:abc" {
		t.Fatalf("digest = %q", got)
	}
}

func TestReadDigestCanonicalizesBuildahImageID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "digest")
	imageID := strings.Repeat("A1", 32)
	if err := os.WriteFile(path, []byte(imageID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "sha256:" + strings.ToLower(imageID); got != want {
		t.Fatalf("digest = %q, want %q", got, want)
	}
}

func TestWriteDigestCreatesParentAndRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "digest")
	if err := WriteDigest(path, " sha256:abc \n"); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "sha256:abc" {
		t.Fatalf("digest = %q", got)
	}
}

func TestWriteImageNameWithDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "image-name")
	if err := WriteImageNameWithDigest(path, "registry.example:5000/team/app", "sha256:manifest"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), "registry.example:5000/team/app@sha256:manifest"; got != want {
		t.Fatalf("image name with digest = %q, want %q", got, want)
	}
}

func TestRegistryType(t *testing.T) {
	t.Parallel()
	for provider, want := range map[string]string{
		"docker": "Docker",
		"gar":    "GAR",
		"ecr":    "ECR",
		"acr":    "ACR",
	} {
		if got := RegistryType(provider); got != want {
			t.Fatalf("RegistryType(%q) = %q, want %q", provider, got, want)
		}
	}
}
