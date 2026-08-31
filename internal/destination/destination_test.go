package destination

import (
	"reflect"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input Input
		want  Result
	}{
		{
			name: "repository and multiple tags",
			input: Input{
				Repository: "example/app",
				Tags:       []string{"1.2.3", "latest"},
			},
			want: Result{
				Repository:   "example/app",
				Tags:         []string{"1.2.3", "latest"},
				Destinations: []string{"example/app:1.2.3", "example/app:latest"},
				Images: []Image{
					{Repository: "example/app", Tag: "1.2.3", Reference: "example/app:1.2.3"},
					{Repository: "example/app", Tag: "latest", Reference: "example/app:latest"},
				},
			},
		},
		{
			name: "custom registry expansion",
			input: Input{
				Registry:         "https://registry.example.com/",
				Repository:       "team/app",
				Tags:             []string{"latest"},
				ExpandRepository: true,
			},
			want: Result{
				Repository:   "registry.example.com/team/app",
				Tags:         []string{"latest"},
				Destinations: []string{"registry.example.com/team/app:latest"},
				Images:       []Image{{Repository: "registry.example.com/team/app", Tag: "latest", Reference: "registry.example.com/team/app:latest"}},
			},
		},
		{
			name: "GAR project namespace expansion",
			input: Input{
				Registry:            "us-central1-docker.pkg.dev/example-project",
				Repository:          "sample-app",
				Tags:                []string{"test"},
				ForceRegistryPrefix: true,
			},
			want: Result{
				Repository:   "us-central1-docker.pkg.dev/example-project/sample-app",
				Tags:         []string{"test"},
				Destinations: []string{"us-central1-docker.pkg.dev/example-project/sample-app:test"},
				Images:       []Image{{Repository: "us-central1-docker.pkg.dev/example-project/sample-app", Tag: "test", Reference: "us-central1-docker.pkg.dev/example-project/sample-app:test"}},
			},
		},
		{
			name: "fully qualified GAR repository is not duplicated",
			input: Input{
				Registry:            "us-central1-docker.pkg.dev/example-project",
				Repository:          "us-central1-docker.pkg.dev/example-project/sample-app",
				Tags:                []string{"test"},
				ForceRegistryPrefix: true,
			},
			want: Result{
				Repository:   "us-central1-docker.pkg.dev/example-project/sample-app",
				Tags:         []string{"test"},
				Destinations: []string{"us-central1-docker.pkg.dev/example-project/sample-app:test"},
				Images:       []Image{{Repository: "us-central1-docker.pkg.dev/example-project/sample-app", Tag: "test", Reference: "us-central1-docker.pkg.dev/example-project/sample-app:test"}},
			},
		},
		{
			name: "does not duplicate custom registry",
			input: Input{
				Registry:         "registry.example.com",
				Repository:       "registry.example.com/team/app",
				Tags:             []string{"latest"},
				ExpandRepository: true,
			},
			want: Result{
				Repository:   "registry.example.com/team/app",
				Tags:         []string{"latest"},
				Destinations: []string{"registry.example.com/team/app:latest"},
				Images:       []Image{{Repository: "registry.example.com/team/app", Tag: "latest", Reference: "registry.example.com/team/app:latest"}},
			},
		},
		{
			name: "does not prefix Docker Hub legacy URL",
			input: Input{
				Registry:         dockerHubV1,
				Repository:       "library/alpine",
				Tags:             []string{"3.22"},
				ExpandRepository: true,
			},
			want: Result{
				Repository:   "library/alpine",
				Tags:         []string{"3.22"},
				Destinations: []string{"library/alpine:3.22"},
				Images:       []Image{{Repository: "library/alpine", Tag: "3.22", Reference: "library/alpine:3.22"}},
			},
		},
		{
			name: "registry port is not mistaken for a tag",
			input: Input{
				Repository: "localhost:5000/team/app",
				Tags:       []string{"dev"},
			},
			want: Result{
				Repository:   "localhost:5000/team/app",
				Tags:         []string{"dev"},
				Destinations: []string{"localhost:5000/team/app:dev"},
				Images:       []Image{{Repository: "localhost:5000/team/app", Tag: "dev", Reference: "localhost:5000/team/app:dev"}},
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := Resolve(test.input)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Resolve() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestResolveDirectDestinations(t *testing.T) {
	t.Parallel()

	got, err := Resolve(Input{Direct: []string{
		"registry.example.com/team/app:one",
		"registry.example.com/team/app:one",
		"other.example.com/team/app:two",
	}})
	if err != nil {
		t.Fatal(err)
	}
	want := Result{
		Destinations: []string{
			"registry.example.com/team/app:one",
			"other.example.com/team/app:two",
		},
		Images: []Image{
			{Repository: "registry.example.com/team/app", Tag: "one", Reference: "registry.example.com/team/app:one"},
			{Repository: "other.example.com/team/app", Tag: "two", Reference: "other.example.com/team/app:two"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Resolve() = %#v, want %#v", got, want)
	}
}

func TestResolveForceRegistryPrefix(t *testing.T) {
	t.Parallel()

	got, err := Resolve(Input{
		Registry:            "123456789012.dkr.ecr.us-east-1.amazonaws.com",
		Repository:          "team/app",
		Tags:                []string{"latest"},
		ForceRegistryPrefix: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Repository != "123456789012.dkr.ecr.us-east-1.amazonaws.com/team/app" {
		t.Fatalf("Resolve() repository = %q", got.Repository)
	}
}

func TestQualifyRepository(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		registry   string
		repository string
		prefix     bool
		want       string
	}{
		{name: "disabled", registry: "registry.example.com", repository: "team/cache:one", want: "team/cache:one"},
		{name: "prefix cache ref with tag", registry: "registry.example.com", repository: "team/cache:one", prefix: true, want: "registry.example.com/team/cache:one"},
		{name: "already qualified", registry: "registry.example.com", repository: "registry.example.com/team/cache:one", prefix: true, want: "registry.example.com/team/cache:one"},
		{name: "Docker Hub is implicit", registry: dockerHubV1, repository: "team/cache:one", prefix: true, want: "team/cache:one"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := QualifyRepository(test.registry, test.repository, test.prefix)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("QualifyRepository() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestResolveRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   Input
		message string
	}{
		{name: "missing repository", input: Input{Tags: []string{"latest"}}, message: "repository is required"},
		{name: "missing tags", input: Input{Repository: "example/app"}, message: "at least one image tag"},
		{name: "repository has tag", input: Input{Repository: "example/app:old", Tags: []string{"new"}}, message: "must not include a tag"},
		{name: "repository has digest", input: Input{Repository: "example/app@sha256:abc", Tags: []string{"latest"}}, message: "must not include a digest"},
		{name: "invalid tag", input: Input{Repository: "example/app", Tags: []string{"not/a/tag"}}, message: "invalid image tag"},
		{name: "direct missing tag", input: Input{Direct: []string{"registry.example.com/team/app"}}, message: "explicit tag"},
		{name: "forced prefix missing registry", input: Input{Repository: "example/app", Tags: []string{"latest"}, ForceRegistryPrefix: true}, message: "registry is required"},
		{name: "fully qualified repository uses another host", input: Input{Registry: "one.example/project", Repository: "two.example/project/app", Tags: []string{"latest"}, ForceRegistryPrefix: true}, message: "does not match repository registry"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := Resolve(test.input)
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Resolve() error = %v, want containing %q", err, test.message)
			}
		})
	}
}

func TestInferRegistry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		repository string
		direct     []string
		want       string
		wantError  string
	}{
		{name: "unqualified Docker Hub repository", repository: "team/app"},
		{name: "qualified repository", repository: "quay.io/team/app", want: "quay.io"},
		{name: "registry port", repository: "localhost:5000/team/app", want: "localhost:5000"},
		{name: "direct destinations share a host", direct: []string{"registry.example/team/one:a", "registry.example/team/two:b"}, want: "registry.example"},
		{name: "direct destinations span hosts", direct: []string{"one.example/team/app:a", "two.example/team/app:b"}, wantError: "multiple registry hosts"},
		{name: "direct destination requires explicit Docker Hub host", direct: []string{"team/app:a"}, wantError: "explicit registry host"},
		{name: "direct Docker Hub and custom destinations span hosts", direct: []string{"docker.io/team/app:a", "evil.example/team/app:b"}, wantError: "multiple registry hosts"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := InferRegistry(test.repository, test.direct)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("InferRegistry() error = %v, want containing %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("InferRegistry() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestReconcileRegistry(t *testing.T) {
	t.Parallel()

	got, err := ReconcileRegistry("https://registry.example/v2/", "registry.example")
	if err != nil {
		t.Fatal(err)
	}
	if got != "registry.example" {
		t.Fatalf("ReconcileRegistry() = %q", got)
	}
	if _, err := ReconcileRegistry("one.example", "two.example"); err == nil {
		t.Fatal("ReconcileRegistry() accepted mismatched hosts")
	}
	if _, err := ReconcileRegistry("https://index.docker.io/v1/", "attacker.example"); err == nil {
		t.Fatal("ReconcileRegistry() rebound explicit Docker Hub credentials to a custom registry")
	}

	got, err = ReconcileRegistry("us-central1-docker.pkg.dev/example-project", "us-central1-docker.pkg.dev")
	if err != nil {
		t.Fatal(err)
	}
	if got != "us-central1-docker.pkg.dev/example-project" {
		t.Fatalf("ReconcileRegistry() = %q, want GAR project prefix", got)
	}
	if host := NormalizeRegistry(got); host != "us-central1-docker.pkg.dev" {
		t.Fatalf("NormalizeRegistry() = %q, want GAR authentication host", host)
	}
}

func TestQualifyRepositoryWithRegistryNamespace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		repository string
		want       string
	}{
		{name: "unqualified repository", repository: "cache", want: "us-central1-docker.pkg.dev/example-project/cache"},
		{name: "qualified repository", repository: "us-central1-docker.pkg.dev/example-project/cache", want: "us-central1-docker.pkg.dev/example-project/cache"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := QualifyRepository("us-central1-docker.pkg.dev/example-project", test.repository, true)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("QualifyRepository() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCacheRegistries(t *testing.T) {
	t.Parallel()
	got := CacheRegistries(
		"us-central1-docker.pkg.dev/project/cache",
		[]string{"type=local,src=/cache", "type=registry,ref=quay.io/team/cache,mode=max", "team/dockerhub-cache"},
		[]string{"type=inline"},
	)
	for _, want := range []string{"us-central1-docker.pkg.dev", "quay.io", "docker.io"} {
		found := false
		for _, value := range got {
			if value == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("CacheRegistries() = %#v, missing %q", got, want)
		}
	}
	if got := CacheRegistries("project/unqualified-cache", nil, nil); len(got) != 0 {
		t.Fatalf("unqualified cache repo inferred before provider resolution: %#v", got)
	}
}
