package registrydigest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func TestResolveInsecureRegistryFallsBackToHTTPUsingDockerConfig(t *testing.T) {
	const username = "registry-user"
	const password = "registry-password"

	registryHandler := registry.New()
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotUsername, gotPassword, ok := request.BasicAuth()
		if !ok || gotUsername != username || gotPassword != password {
			writer.Header().Set("WWW-Authenticate", `Basic realm="test registry"`)
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		registryHandler.ServeHTTP(writer, request)
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.Start()
	defer server.Close()

	host := mappedRegistryHost(t, server)
	transport := mappedTransport(t, server)
	configureDockerAuth(t, host, username, password)

	destinations := []string{host + "/team/app:first", host + "/team/app:second"}
	wantDigests := make(map[string]string, len(destinations))
	for _, destination := range destinations {
		manifestDigest, _ := pushTestImage(t, destination, username, password, transport, true)
		wantDigests[destination] = manifestDigest
	}

	got, err := resolve(context.Background(), Options{
		Destinations:       destinations,
		InsecureRegistries: []string{"https://" + host + "/v2/"},
	}, dependencies{
		head:      remote.Head,
		get:       remote.Get,
		transport: transport,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, destination := range destinations {
		if got[destination] != wantDigests[destination] {
			t.Fatalf("digest for %q = %q, want manifest digest %q", destination, got[destination], wantDigests[destination])
		}
	}
	if got[destinations[0]] == got[destinations[1]] {
		t.Fatalf("distinct pushed manifests resolved to the same digest %q", got[destinations[0]])
	}
}

func TestResolveInsecureTLSRegistryReturnsManifestDigestUsingDockerConfig(t *testing.T) {
	const username = "registry-user"
	const password = "registry-password"

	registryHandler := registry.New()
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotUsername, gotPassword, ok := request.BasicAuth()
		if !ok || gotUsername != username || gotPassword != password {
			writer.Header().Set("WWW-Authenticate", `Basic realm="test registry"`)
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		registryHandler.ServeHTTP(writer, request)
	}))
	defer server.Close()

	host := mappedRegistryHost(t, server)
	transport := mappedTransport(t, server)
	configureDockerAuth(t, host, username, password)

	destination := host + "/team/app:tls"
	pushTransport, err := transportWithoutTLSVerification(transport)
	if err != nil {
		t.Fatal(err)
	}
	wantManifestDigest, configDigest := pushTestImage(t, destination, username, password, pushTransport, false)
	if wantManifestDigest == configDigest {
		t.Fatalf("test image manifest and config unexpectedly share digest %s", wantManifestDigest)
	}

	got, err := resolve(context.Background(), Options{
		Destinations:       []string{destination},
		InsecureRegistries: []string{host},
	}, dependencies{
		head:      remote.Head,
		get:       remote.Get,
		transport: transport,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[destination] != wantManifestDigest {
		t.Fatalf("digest = %q, want manifest digest %q (not config digest %q)", got[destination], wantManifestDigest, configDigest)
	}
}

func TestResolveSecureRegistryRejectsUntrustedTLS(t *testing.T) {
	server := httptest.NewTLSServer(registry.New())
	defer server.Close()

	host := mappedRegistryHost(t, server)
	_, err := resolve(context.Background(), Options{
		Destinations: []string{host + "/team/app:secure"},
	}, dependencies{
		head:      remote.Head,
		get:       remote.Get,
		transport: mappedTransport(t, server),
	})
	if err == nil {
		t.Fatal("Resolve() unexpectedly accepted an untrusted certificate for a secure registry")
	}
	if !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("Resolve() error %q does not report TLS certificate verification", err)
	}
}

func TestResolveFallsBackToGetBeforePlainHTTP(t *testing.T) {
	digest := mustHash(t, "sha256:"+strings.Repeat("a", 64))
	headCalls := 0
	getCalls := 0
	got, err := resolve(context.Background(), Options{
		Destinations:       []string{"registry.example:5000/team/app:test"},
		InsecureRegistries: []string{"https://registry.example:5000/v2/"},
	}, dependencies{
		head: func(reference name.Reference, _ ...remote.Option) (*v1.Descriptor, error) {
			headCalls++
			if reference.Context().Registry.Scheme() != "https" {
				t.Fatalf("registry scheme = %q, want https", reference.Context().Registry.Scheme())
			}
			return nil, errors.New("HEAD unsupported")
		},
		get: func(reference name.Reference, _ ...remote.Option) (*remote.Descriptor, error) {
			getCalls++
			if reference.Context().Registry.Scheme() != "https" {
				t.Fatalf("registry scheme = %q, want https", reference.Context().Registry.Scheme())
			}
			return &remote.Descriptor{Descriptor: v1.Descriptor{Digest: digest}}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if headCalls != 1 || getCalls != 1 {
		t.Fatalf("lookup calls = HEAD %d, GET %d; want 1 each", headCalls, getCalls)
	}
	if got["registry.example:5000/team/app:test"] != digest.String() {
		t.Fatalf("digest = %q, want %q", got["registry.example:5000/team/app:test"], digest)
	}
}

func TestResolveInsecureLookupFallsBackFromHTTPSToHTTP(t *testing.T) {
	digest := mustHash(t, "sha256:"+strings.Repeat("c", 64))
	var calls []string
	got, err := resolve(context.Background(), Options{
		Destinations: []string{"registry.example:5000/team/app:test"},
		Insecure:     true,
	}, dependencies{
		head: func(reference name.Reference, _ ...remote.Option) (*v1.Descriptor, error) {
			scheme := reference.Context().Registry.Scheme()
			calls = append(calls, "HEAD "+scheme)
			if scheme == "http" {
				return &v1.Descriptor{Digest: digest}, nil
			}
			return nil, errors.New("HTTPS HEAD failed")
		},
		get: func(reference name.Reference, _ ...remote.Option) (*remote.Descriptor, error) {
			scheme := reference.Context().Registry.Scheme()
			calls = append(calls, "GET "+scheme)
			return nil, errors.New("HTTPS GET failed")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["registry.example:5000/team/app:test"] != digest.String() {
		t.Fatalf("digest = %q, want %q", got["registry.example:5000/team/app:test"], digest)
	}
	wantCalls := []string{"HEAD https", "GET https", "HEAD http"}
	if strings.Join(calls, ",") != strings.Join(wantCalls, ",") {
		t.Fatalf("lookup calls = %v, want %v", calls, wantCalls)
	}
}

func TestResolveSecureRegistryUsesHTTPS(t *testing.T) {
	digest := mustHash(t, "sha256:"+strings.Repeat("b", 64))
	_, err := resolve(context.Background(), Options{
		Destinations: []string{"registry.example/team/app:test"},
	}, dependencies{
		head: func(reference name.Reference, _ ...remote.Option) (*v1.Descriptor, error) {
			if got := reference.Context().Registry.Scheme(); got != "https" {
				t.Fatalf("registry scheme = %q, want https", got)
			}
			return &v1.Descriptor{Digest: digest}, nil
		},
		get: func(name.Reference, ...remote.Option) (*remote.Descriptor, error) {
			t.Fatal("GET called after successful HEAD")
			return nil, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestResolveReportsHeadAndGetFailure(t *testing.T) {
	_, err := resolve(context.Background(), Options{
		Destinations: []string{"registry.example/team/app:test"},
	}, dependencies{
		head: func(name.Reference, ...remote.Option) (*v1.Descriptor, error) {
			return nil, errors.New("head denied")
		},
		get: func(name.Reference, ...remote.Option) (*remote.Descriptor, error) {
			return nil, errors.New("get denied")
		},
	})
	if err == nil {
		t.Fatal("Resolve() unexpectedly succeeded")
	}
	for _, expected := range []string{"registry.example/team/app:test", "head denied", "get denied"} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("Resolve() error %q does not contain %q", err, expected)
		}
	}
}

func mustHash(t *testing.T, value string) v1.Hash {
	t.Helper()
	hash, err := v1.NewHash(value)
	if err != nil {
		t.Fatal(err)
	}
	return hash
}

func mappedRegistryHost(t *testing.T, server *httptest.Server) string {
	t.Helper()
	_, port, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	return net.JoinHostPort("registry.example", port)
}

func mappedTransport(t *testing.T, server *httptest.Server) *http.Transport {
	t.Helper()
	transport, ok := remote.DefaultTransport.(*http.Transport)
	if !ok {
		t.Fatalf("remote.DefaultTransport has unexpected type %T", remote.DefaultTransport)
	}
	clone := transport.Clone()
	clone.Proxy = nil
	target := server.Listener.Addr().String()
	dialer := &net.Dialer{}
	clone.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, target)
	}
	return clone
}

func configureDockerAuth(t *testing.T, host, username, password string) {
	t.Helper()
	configDir := t.TempDir()
	authValue := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	configData, err := json.Marshal(map[string]any{
		"auths": map[string]any{
			host: map[string]string{"auth": authValue},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), configData, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_CONFIG", configDir)
}

func pushTestImage(
	t *testing.T,
	destination, username, password string,
	transport http.RoundTripper,
	plainHTTP bool,
) (string, string) {
	t.Helper()
	image, err := random.Image(1024, 1)
	if err != nil {
		t.Fatal(err)
	}
	manifestDigest, err := image.Digest()
	if err != nil {
		t.Fatal(err)
	}
	configDigest, err := image.ConfigName()
	if err != nil {
		t.Fatal(err)
	}
	nameOptions := []name.Option{name.StrictValidation}
	if plainHTTP {
		nameOptions = append(nameOptions, name.Insecure)
	}
	tag, err := name.NewTag(destination, nameOptions...)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(
		tag,
		image,
		remote.WithAuth(&authn.Basic{Username: username, Password: password}),
		remote.WithTransport(transport),
	); err != nil {
		t.Fatal(err)
	}
	return manifestDigest.String(), configDigest.String()
}
