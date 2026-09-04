package releaseverify

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

func TestVerifyRegistryImages(t *testing.T) {
	const (
		username = "release-user"
		password = "release-secret"
		revision = "0123456789abcdef0123456789abcdef01234567"
		tag      = "v0.2.0-alpha"
	)

	authorization := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
	var authenticatedRequests atomic.Int64
	registryHandler := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != authorization {
			writer.Header().Set("WWW-Authenticate", `Basic realm="release-test"`)
			http.Error(writer, "authentication required", http.StatusUnauthorized)
			return
		}
		authenticatedRequests.Add(1)
		registryHandler.ServeHTTP(writer, request)
	}))
	t.Cleanup(server.Close)

	host := strings.TrimPrefix(server.URL, "http://")
	prefix := host + "/plugins"
	authenticator := authn.FromConfig(authn.AuthConfig{Username: username, Password: password})
	for _, provider := range providers {
		for _, architecture := range architectures {
			image := fixtureImage(t, provider, architecture, revision)
			reference := fmt.Sprintf(
				"%s/%s:%s",
				prefix,
				provider.repository,
				architectureTag("0.2.0-alpha", architecture),
			)
			tagReference, err := name.NewTag(reference, name.StrictValidation, name.Insecure)
			if err != nil {
				t.Fatalf("parse fixture reference %q: %v", reference, err)
			}
			if err := remote.Write(tagReference, image, remote.WithAuth(authenticator)); err != nil {
				t.Fatalf("publish fixture image %q: %v", reference, err)
			}
		}
	}

	var output bytes.Buffer
	err := Verify(context.Background(), Options{
		RepositoryPrefix: prefix,
		ReleaseTag:       tag,
		Revision:         revision,
		Username:         username,
		Password:         password,
		Insecure:         true,
		Writer:           &output,
	})
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if authenticatedRequests.Load() == 0 {
		t.Fatal("Verify() did not authenticate to the registry")
	}
	if !strings.Contains(output.String(), "verified 8 architecture images with unique provider digests") {
		t.Fatalf("Verify() output = %q", output.String())
	}
	for _, secret := range []string{username, password, authorization} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("Verify() output contains credential %q", secret)
		}
	}
}

func TestValidateConfig(t *testing.T) {
	const revision = "0123456789abcdef0123456789abcdef01234567"
	provider := providers[1]
	valid := fixtureConfig(provider, "arm64", revision)

	tests := []struct {
		name     string
		mutate   func(*v1.ConfigFile)
		contains string
	}{
		{name: "architecture", mutate: func(config *v1.ConfigFile) { config.Architecture = "amd64" }, contains: "architecture"},
		{name: "operating system", mutate: func(config *v1.ConfigFile) { config.OS = "windows" }, contains: "operating system"},
		{name: "runtime user", mutate: func(config *v1.ConfigFile) { config.Config.User = "1000:1000" }, contains: "runtime user"},
		{name: "missing XDG runtime directory", mutate: func(config *v1.ConfigFile) {
			config.Config.Env = []string{"FIXTURE=value"}
		}, contains: "XDG_RUNTIME_DIR is not configured"},
		{name: "wrong XDG runtime directory", mutate: func(config *v1.ConfigFile) {
			config.Config.Env = append(config.Config.Env, "XDG_RUNTIME_DIR=/run/user/1000")
		}, contains: "XDG_RUNTIME_DIR is"},
		{name: "entrypoint", mutate: func(config *v1.ConfigFile) { config.Config.Entrypoint = []string{"/usr/local/bin/kimia-ecr"} }, contains: "entrypoint"},
		{name: "title", mutate: func(config *v1.ConfigFile) {
			config.Config.Labels["org.opencontainers.image.title"] = "drone-kimia-ecr"
		}, contains: "title"},
		{name: "revision", mutate: func(config *v1.ConfigFile) { config.Config.Labels["org.opencontainers.image.revision"] = "wrong" }, contains: "revision"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid.DeepCopy()
			test.mutate(config)
			err := validateConfig(provider, "arm64", revision, config)
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("validateConfig() error = %v, want text %q", err, test.contains)
			}
		})
	}
}

func TestVerifyUniqueDigestsRejectsProviderReuse(t *testing.T) {
	images := []verifiedImage{
		{reference: "plugins/kimia:linux-amd64", provider: "docker", architecture: "amd64", digest: "sha256:duplicate"},
		{reference: "plugins/kimia-gar:linux-amd64", provider: "gar", architecture: "amd64", digest: "sha256:duplicate"},
	}
	err := verifyUniqueDigests(images)
	if err == nil || !strings.Contains(err.Error(), "reuses digest") {
		t.Fatalf("verifyUniqueDigests() error = %v, want digest reuse error", err)
	}
}

func TestVerifyCompatibilityEntrypoint(t *testing.T) {
	provider := providers[1]
	binary := tar.Header{
		Name:     "usr/local/bin/kimia-gar",
		Typeflag: tar.TypeReg,
		Mode:     0o755,
		Size:     1,
	}

	tests := []struct {
		name     string
		headers  []tar.Header
		contains string
	}{
		{
			name: "symlink",
			headers: []tar.Header{
				binary,
				{Name: "kaniko/kaniko-gar", Typeflag: tar.TypeSymlink, Mode: 0o777, Linkname: "/usr/local/bin/kimia-gar"},
			},
		},
		{
			name: "executable equivalent",
			headers: []tar.Header{
				binary,
				{Name: "kaniko/kaniko-gar", Typeflag: tar.TypeReg, Mode: 0o755, Size: 1},
			},
		},
		{
			name:     "missing alias",
			headers:  []tar.Header{binary},
			contains: "entrypoint /kaniko/kaniko-gar is missing",
		},
		{
			name: "wrong symlink",
			headers: []tar.Header{
				binary,
				{Name: "kaniko/kaniko-gar", Typeflag: tar.TypeSymlink, Mode: 0o777, Linkname: "/usr/local/bin/kimia-ecr"},
			},
			contains: "resolves to",
		},
		{
			name: "non-executable alias",
			headers: []tar.Header{
				binary,
				{Name: "kaniko/kaniko-gar", Typeflag: tar.TypeReg, Mode: 0o644, Size: 1},
			},
			contains: "is not executable",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			image := fixtureFilesystemImage(t, test.headers...)
			err := verifyCompatibilityEntrypoint(image, provider)
			if test.contains == "" && err != nil {
				t.Fatalf("verifyCompatibilityEntrypoint() error = %v", err)
			}
			if test.contains != "" && (err == nil || !strings.Contains(err.Error(), test.contains)) {
				t.Fatalf("verifyCompatibilityEntrypoint() error = %v, want text %q", err, test.contains)
			}
		})
	}
}

func TestVerifyRuntimeDirectory(t *testing.T) {
	valid := tar.Header{
		Name:     "tmp/run/",
		Typeflag: tar.TypeDir,
		Mode:     0o700,
		Uid:      0,
		Gid:      0,
	}

	tests := []struct {
		name     string
		headers  []tar.Header
		contains string
	}{
		{name: "directory", headers: []tar.Header{valid}},
		{name: "missing", contains: "runtime directory /tmp/run is missing"},
		{
			name: "regular file",
			headers: []tar.Header{{
				Name:     "tmp/run",
				Typeflag: tar.TypeReg,
				Mode:     0o700,
			}},
			contains: "is not a directory",
		},
		{
			name: "wrong owner",
			headers: []tar.Header{{
				Name:     "tmp/run/",
				Typeflag: tar.TypeDir,
				Mode:     0o700,
				Uid:      1000,
				Gid:      1000,
			}},
			contains: "owned by 1000:1000",
		},
		{
			name: "wrong mode",
			headers: []tar.Header{{
				Name:     "tmp/run/",
				Typeflag: tar.TypeDir,
				Mode:     0o755,
			}},
			contains: "mode is 0755",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			image := fixtureFilesystemImage(t, test.headers...)
			err := verifyRuntimeDirectory(image)
			if test.contains == "" && err != nil {
				t.Fatalf("verifyRuntimeDirectory() error = %v", err)
			}
			if test.contains != "" && (err == nil || !strings.Contains(err.Error(), test.contains)) {
				t.Fatalf("verifyRuntimeDirectory() error = %v, want text %q", err, test.contains)
			}
		})
	}
}

func TestArchitectureTag(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		arch     string
		expected string
	}{
		{name: "tag", input: "v0.2.0-alpha", arch: "amd64", expected: "0.2.0-alpha-linux-amd64"},
		{name: "branch null", input: "null", arch: "arm64", expected: "linux-arm64"},
		{name: "branch empty", input: "", arch: "amd64", expected: "linux-amd64"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tag, err := normalizeReleaseTag(test.input)
			if err != nil {
				t.Fatalf("normalizeReleaseTag() error = %v", err)
			}
			if actual := architectureTag(tag, test.arch); actual != test.expected {
				t.Fatalf("architectureTag() = %q, want %q", actual, test.expected)
			}
		})
	}
}

func fixtureImage(t *testing.T, provider providerSpec, architecture, revision string) v1.Image {
	t.Helper()
	image, err := mutate.ConfigFile(empty.Image, fixtureConfig(provider, architecture, revision))
	if err != nil {
		t.Fatalf("create fixture image: %v", err)
	}
	layer := fixtureLayer(t,
		tar.Header{
			Name:     "tmp/run/",
			Typeflag: tar.TypeDir,
			Mode:     0o700,
			Uid:      0,
			Gid:      0,
		},
		tar.Header{
			Name:     "usr/local/bin/kimia-" + provider.name,
			Typeflag: tar.TypeReg,
			Mode:     0o755,
			Size:     1,
		},
		tar.Header{
			Name:     "kaniko/kaniko-" + provider.name,
			Typeflag: tar.TypeSymlink,
			Mode:     0o777,
			Linkname: "/usr/local/bin/kimia-" + provider.name,
		},
	)
	image, err = mutate.AppendLayers(image, layer)
	if err != nil {
		t.Fatalf("append fixture layer: %v", err)
	}
	return image
}

func fixtureConfig(provider providerSpec, architecture, revision string) *v1.ConfigFile {
	return &v1.ConfigFile{
		Architecture: architecture,
		OS:           "linux",
		Config: v1.Config{
			Entrypoint: []string{"/usr/local/bin/kimia-" + provider.name},
			User:       expectedRuntimeUser,
			Env: []string{
				"FIXTURE=" + provider.name + "-" + architecture,
				"XDG_RUNTIME_DIR=" + expectedXDGRuntimeDir,
			},
			Labels: map[string]string{
				"org.opencontainers.image.title":    provider.title,
				"org.opencontainers.image.revision": revision,
			},
		},
	}
}

func fixtureFilesystemImage(t *testing.T, headers ...tar.Header) v1.Image {
	t.Helper()
	image, err := mutate.AppendLayers(empty.Image, fixtureLayer(t, headers...))
	if err != nil {
		t.Fatalf("append fixture filesystem layer: %v", err)
	}
	return image
}

func fixtureLayer(t *testing.T, headers ...tar.Header) v1.Layer {
	t.Helper()
	var contents bytes.Buffer
	tarWriter := tar.NewWriter(&contents)
	for index := range headers {
		header := headers[index]
		if err := tarWriter.WriteHeader(&header); err != nil {
			t.Fatalf("write fixture header %q: %v", header.Name, err)
		}
		if header.Size > 0 {
			if _, err := tarWriter.Write(bytes.Repeat([]byte{'x'}, int(header.Size))); err != nil {
				t.Fatalf("write fixture contents %q: %v", header.Name, err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatalf("close fixture tar: %v", err)
	}
	data := append([]byte(nil), contents.Bytes()...)
	layer, err := tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	})
	if err != nil {
		t.Fatalf("create fixture layer: %v", err)
	}
	return layer
}
