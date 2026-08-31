package result

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joho/godotenv"
)

const artifactKind = "docker/v1"

type Image struct {
	Image  string `json:"image"`
	Digest string `json:"digest"`
}

type Artifact struct {
	Kind string       `json:"kind"`
	Data ArtifactData `json:"data"`
}

type ArtifactData struct {
	RegistryType string  `json:"registryType"`
	RegistryURL  string  `json:"registryUrl"`
	Images       []Image `json:"images"`
}

func ReadDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read digest file %q: %w", path, err)
	}
	digest := strings.TrimSpace(string(data))
	if digest == "" {
		return "", fmt.Errorf("digest file %q is empty", path)
	}
	return digest, nil
}

// WriteDigest records a registry manifest digest for operations, such as
// push-only, that do not ask Kimia to create its normal digest output file.
func WriteDigest(path, digest string) error {
	digest = strings.TrimSpace(digest)
	if digest == "" {
		return fmt.Errorf("image digest is empty")
	}
	return writeFile(path, []byte(digest+"\n"), 0o644)
}

func WriteArtifact(path, registryType, registryURL, digest string, destinations []string) error {
	images := make([]Image, 0, len(destinations))
	for _, destination := range destinations {
		images = append(images, Image{Image: destination, Digest: digest})
	}
	data, err := json.MarshalIndent(Artifact{
		Kind: artifactKind,
		Data: ArtifactData{
			RegistryType: registryType,
			RegistryURL:  registryURL,
			Images:       images,
		},
	}, "", "\t")
	if err != nil {
		return fmt.Errorf("marshal plugin artifact: %w", err)
	}
	return writeFile(path, data, 0o644)
}

func WriteDroneOutput(path, digest, tarPath string) error {
	values := make(map[string]string, 2)
	if digest != "" {
		values["digest"] = digest
	}
	if tarPath != "" {
		if _, err := os.Stat(tarPath); err != nil {
			return fmt.Errorf("stat tar output %q: %w", tarPath, err)
		}
		values["IMAGE_TAR_PATH"] = tarPath
	}
	data, err := godotenv.Marshal(values)
	if err != nil {
		return fmt.Errorf("marshal DRONE_OUTPUT: %w", err)
	}
	return writeFile(path, []byte(data+"\n"), 0o644)
}

func RegistryType(provider string) string {
	switch strings.ToLower(provider) {
	case "gar":
		return "GAR"
	case "ecr":
		return "ECR"
	case "acr":
		return "ACR"
	default:
		return "Docker"
	}
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create output directory %q: %w", dir, err)
	}
	temporary, err := os.CreateTemp(dir, ".drone-kimia-*")
	if err != nil {
		return fmt.Errorf("create temporary output for %q: %w", path, err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		temporary.Close()
		return fmt.Errorf("set output permissions for %q: %w", path, err)
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return fmt.Errorf("write output %q: %w", path, err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close output %q: %w", path, err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace output %q: %w", path, err)
	}
	return nil
}
