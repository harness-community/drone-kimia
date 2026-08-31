package archivepush

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

func TestPushLoadsOncePushesEveryDestinationAndReturnsDigest(t *testing.T) {
	t.Parallel()

	source := writeArchive(t, `[{"Config":"config.json","RepoTags":null,"Layers":[]}]`)
	destinations := []string{
		"registry.example.com/team/app:one",
		"localhost:5000/team/app:two",
		"team/app:latest",
	}
	type contextKey string
	ctx := context.WithValue(context.Background(), contextKey("test"), "value")

	loadCalls := 0
	type pushCall struct {
		destination string
		insecure    bool
	}
	var pushCalls []pushCall
	deps := dependencies{
		load: func(path string) (v1.Image, error) {
			loadCalls++
			if path != source {
				t.Fatalf("load path = %q, want %q", path, source)
			}
			return empty.Image, nil
		},
		push: func(got context.Context, image v1.Image, destination string, insecure bool) error {
			if got != ctx {
				t.Fatal("push did not receive caller context")
			}
			if image != empty.Image {
				t.Fatal("push did not receive loaded image")
			}
			pushCalls = append(pushCalls, pushCall{destination: destination, insecure: insecure})
			return nil
		},
	}

	var output bytes.Buffer
	digest, err := push(ctx, Options{
		SourceTarPath: source,
		Destinations:  destinations,
		InsecureRegistries: []string{
			"https://localhost:5000/v1/",
			"index.docker.io",
		},
		Writer: &output,
	}, deps)
	if err != nil {
		t.Fatal(err)
	}
	wantDigest, err := empty.Image.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if digest != wantDigest.String() {
		t.Fatalf("digest = %q, want %q", digest, wantDigest)
	}
	if loadCalls != 1 {
		t.Fatalf("load calls = %d, want 1", loadCalls)
	}
	wantCalls := []pushCall{
		{destination: destinations[0], insecure: false},
		{destination: destinations[1], insecure: true},
		{destination: destinations[2], insecure: true},
	}
	if !reflect.DeepEqual(pushCalls, wantCalls) {
		t.Fatalf("push calls = %#v, want %#v", pushCalls, wantCalls)
	}
	for _, destination := range destinations {
		if !strings.Contains(output.String(), "Successfully pushed image to "+destination+"\n") {
			t.Fatalf("output %q does not contain destination %q", output.String(), destination)
		}
	}
}

func TestPushGlobalInsecureAppliesToEveryDestination(t *testing.T) {
	t.Parallel()

	targets, err := resolveTargets([]string{
		"registry.example.com/team/app:one",
		"other.example.com/team/app:two",
	}, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		if !target.insecure {
			t.Fatalf("target %q is not insecure", target.reference)
		}
	}
}

func TestPushRejectsInvalidArchivesBeforePushing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		manifest *string
		contains string
	}{
		{name: "missing manifest", contains: "load source image archive"},
		{name: "invalid manifest", manifest: stringPointer(`{`), contains: "load source image archive"},
		{name: "no images", manifest: stringPointer(`[]`), contains: "must contain exactly one image"},
		{name: "multiple images", manifest: stringPointer(`[{},{}]`), contains: "must contain exactly one image"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := writeArchiveWithOptionalManifest(t, test.manifest)
			loadCalls := 0
			_, err := push(context.Background(), Options{
				SourceTarPath: source,
				Destinations:  []string{"registry.example.com/team/app:test"},
			}, dependencies{
				load: func(path string) (v1.Image, error) {
					loadCalls++
					return defaultDependencies.load(path)
				},
				push: func(context.Context, v1.Image, string, bool) error {
					t.Fatal("push called for invalid archive")
					return nil
				},
			})
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %v, want containing %q", err, test.contains)
			}
			if loadCalls != 1 {
				t.Fatalf("load calls = %d, want 1", loadCalls)
			}
		})
	}
}

func TestPushValidatesInputsBeforeLoading(t *testing.T) {
	t.Parallel()

	validArchive := writeArchive(t, `[{}]`)
	directory := t.TempDir()
	tests := []struct {
		name         string
		ctx          context.Context
		source       string
		destinations []string
		contains     string
	}{
		{name: "nil context", source: validArchive, destinations: []string{"example.com/app:test"}, contains: "context is required"},
		{name: "cancelled context", ctx: cancelledContext(), source: validArchive, destinations: []string{"example.com/app:test"}, contains: "context canceled"},
		{name: "missing source", ctx: context.Background(), destinations: []string{"example.com/app:test"}, contains: "path is required"},
		{name: "directory source", ctx: context.Background(), source: directory, destinations: []string{"example.com/app:test"}, contains: "regular file"},
		{name: "missing destination", ctx: context.Background(), source: validArchive, contains: "at least one image destination"},
		{name: "invalid destination", ctx: context.Background(), source: validArchive, destinations: []string{"not a reference"}, contains: "invalid image destination"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			loadCalls := 0
			_, err := push(test.ctx, Options{
				SourceTarPath: test.source,
				Destinations:  test.destinations,
			}, dependencies{
				load: func(string) (v1.Image, error) {
					loadCalls++
					return empty.Image, nil
				},
				push: func(context.Context, v1.Image, string, bool) error { return nil },
			})
			if err == nil || !strings.Contains(err.Error(), test.contains) {
				t.Fatalf("error = %v, want containing %q", err, test.contains)
			}
			if loadCalls != 0 {
				t.Fatalf("load calls = %d, want 0", loadCalls)
			}
		})
	}
}

func TestPushWrapsLoadAndDestinationErrors(t *testing.T) {
	t.Parallel()

	source := writeArchive(t, `[{}]`)
	loadError := errors.New("load failed")
	_, err := push(context.Background(), Options{
		SourceTarPath: source,
		Destinations:  []string{"registry.example.com/team/app:test"},
	}, dependencies{
		load: func(string) (v1.Image, error) { return nil, loadError },
		push: func(context.Context, v1.Image, string, bool) error { return nil },
	})
	if !errors.Is(err, loadError) || !strings.Contains(err.Error(), "load source image archive") {
		t.Fatalf("load error = %v", err)
	}

	pushError := errors.New("push failed")
	_, err = push(context.Background(), Options{
		SourceTarPath: source,
		Destinations:  []string{"registry.example.com/team/app:test"},
	}, dependencies{
		load: func(string) (v1.Image, error) { return empty.Image, nil },
		push: func(context.Context, v1.Image, string, bool) error { return pushError },
	})
	if !errors.Is(err, pushError) || !strings.Contains(err.Error(), `to "registry.example.com/team/app:test"`) {
		t.Fatalf("push error = %v", err)
	}
}

func TestPushPublishesUntaggedKimiaStyleArchive(t *testing.T) {
	t.Parallel()

	image, err := random.Image(1024, 1)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := name.NewTag("example.com/source/image:test")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	taggedArchive := filepath.Join(directory, "tagged.tar")
	if err := tarball.WriteToFile(taggedArchive, tag, image); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(directory, "untagged.tar")
	rewriteArchiveWithoutRepoTags(t, taggedArchive, source)

	registryServer := httptest.NewServer(registry.New(registry.Logger(log.New(io.Discard, "", 0))))
	defer registryServer.Close()
	registryHost := strings.TrimPrefix(registryServer.URL, "http://")
	destination := registryHost + "/team/app:test"

	digest, err := Push(context.Background(), Options{
		SourceTarPath:      source,
		Destinations:       []string{destination},
		InsecureRegistries: []string{registryServer.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	remoteReference, err := name.ParseReference(destination, name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	pushed, err := remote.Image(remoteReference, remote.WithAuth(authn.Anonymous))
	if err != nil {
		t.Fatal(err)
	}
	pushedDigest, err := pushed.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if digest != pushedDigest.String() {
		t.Fatalf("digest = %q, pushed digest = %q", digest, pushedDigest)
	}
}

func writeArchive(t *testing.T, manifest string) string {
	t.Helper()
	return writeArchiveWithOptionalManifest(t, &manifest)
}

func writeArchiveWithOptionalManifest(t *testing.T, manifest *string) string {
	t.Helper()
	archivePath := filepath.Join(t.TempDir(), "image.tar")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := tar.NewWriter(file)
	if manifest == nil {
		if err := writer.WriteHeader(&tar.Header{Name: "placeholder", Mode: 0o600, Size: 0}); err != nil {
			t.Fatal(err)
		}
	} else {
		data := []byte(*manifest)
		if err := writer.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o600, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return archivePath
}

func rewriteArchiveWithoutRepoTags(t *testing.T, source, destination string) {
	t.Helper()
	input, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := os.Create(destination)
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(input)
	writer := tar.NewWriter(output)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		copied := *header
		if path.Clean(header.Name) == "manifest.json" {
			var manifest tarball.Manifest
			if err := json.NewDecoder(reader).Decode(&manifest); err != nil {
				t.Fatal(err)
			}
			for index := range manifest {
				manifest[index].RepoTags = nil
			}
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			copied.Size = int64(len(data))
			if err := writer.WriteHeader(&copied); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.Write(data); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := writer.WriteHeader(&copied); err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(writer, reader); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
}

func stringPointer(value string) *string {
	return &value
}

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}
