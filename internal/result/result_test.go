package result

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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
