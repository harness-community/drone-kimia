package destination

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const dockerHubV1 = "https://index.docker.io/v1/"

var tagPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)

// Input contains the repository inputs accepted by the Docker and Kaniko
// compatibility entrypoints. Tags must already have been resolved (including
// automatic or semantic-version expansion) before Resolve is called.
type Input struct {
	Registry            string
	Repository          string
	Tags                []string
	Direct              []string
	ExpandRepository    bool
	ForceRegistryPrefix bool
}

// Image is one resolved repository/tag pair. Direct destinations can target
// different repositories, so callers that produce artifacts should use Images
// rather than assuming Result.Repository is always populated.
type Image struct {
	Repository string
	Tag        string
	Reference  string
}

// Result is the normalized image destination set passed to Kimia and reused
// when writing Harness artifact metadata.
type Result struct {
	Repository   string
	Tags         []string
	Destinations []string
	Images       []Image
}

// InferRegistry returns the single explicit registry host used by a repository
// or set of direct destinations. Docker Hub-style unqualified repositories do
// not have an explicit host. A connector credential can only be prepared for
// one host, so a direct destination set spanning multiple registries is
// rejected instead of silently authenticating only the first one.
func InferRegistry(repository string, direct []string) (string, error) {
	registries := make(map[string]struct{})
	if len(direct) > 0 {
		for _, reference := range direct {
			repository, _, err := splitReference(strings.TrimSpace(reference))
			if err != nil {
				return "", err
			}
			registry := RegistryHost(repository)
			if registry == "" {
				return "", fmt.Errorf("direct destination %q must include an explicit registry host; use docker.io for Docker Hub", reference)
			}
			registries[registry] = struct{}{}
		}
	} else if registry := RegistryHost(strings.TrimSpace(repository)); registry != "" {
		registries[registry] = struct{}{}
	}

	if len(registries) > 1 {
		return "", fmt.Errorf("destinations span multiple registry hosts; use one registry per plugin step or provide a separate step for each registry")
	}
	for registry := range registries {
		return registry, nil
	}
	return "", nil
}

// CacheRegistries returns the explicit registry hosts referenced by remote
// cache configuration. Unqualified import/export references resolve to Docker
// Hub. An unqualified CacheRepo is omitted because the caller may deliberately
// qualify it with the selected cloud provider registry later.
func CacheRegistries(cacheRepo string, imports, exports []string) []string {
	seen := make(map[string]struct{})
	add := func(reference string, unqualifiedDockerHub bool) {
		reference = strings.TrimSpace(reference)
		if reference == "" {
			return
		}
		registry := RegistryHost(reference)
		if registry == "" && unqualifiedDockerHub {
			registry = "docker.io"
		}
		if registry != "" {
			seen[registry] = struct{}{}
		}
	}
	add(cacheRepo, false)
	for _, specification := range append(append([]string{}, imports...), exports...) {
		if reference, ok := registryCacheReference(specification); ok {
			add(reference, true)
		}
	}
	result := make([]string, 0, len(seen))
	for registry := range seen {
		result = append(result, registry)
	}
	return result
}

func registryCacheReference(specification string) (string, bool) {
	specification = strings.TrimSpace(specification)
	if specification == "" {
		return "", false
	}
	if !strings.HasPrefix(strings.ToLower(specification), "type=") {
		return specification, true
	}
	parts := strings.Split(specification, ",")
	if len(parts) == 0 || !strings.EqualFold(strings.TrimSpace(parts[0]), "type=registry") {
		return "", false
	}
	for _, part := range parts[1:] {
		key, value, found := strings.Cut(part, "=")
		if found && strings.EqualFold(strings.TrimSpace(key), "ref") {
			return strings.TrimSpace(value), strings.TrimSpace(value) != ""
		}
	}
	return "", false
}

// ReconcileRegistry uses an inferred destination host when PLUGIN_REGISTRY is
// absent and rejects mismatches when both are present.
func ReconcileRegistry(configured, inferred string) (string, error) {
	configuredHost := NormalizeRegistry(configured)
	inferredHost := NormalizeRegistry(inferred)
	if configuredHost == "" {
		return inferredHost, nil
	}
	if inferredHost != "" && !sameRegistry(configuredHost, inferredHost) {
		return "", fmt.Errorf("configured registry %q does not match destination registry %q", configuredHost, inferredHost)
	}
	return configuredHost, nil
}

// RegistryHost extracts an explicit registry host from an image repository.
// Docker's reference rules treat the first path component as a registry only
// when it contains a dot or port, or is localhost.
func RegistryHost(repository string) string {
	repository = strings.TrimSpace(repository)
	first, remainder, hasSlash := strings.Cut(repository, "/")
	if !hasSlash || remainder == "" {
		return ""
	}
	lower := strings.ToLower(first)
	if lower == "localhost" || strings.Contains(first, ".") || strings.Contains(first, ":") || strings.HasPrefix(first, "[") {
		return NormalizeRegistry(first)
	}
	return ""
}

// NormalizeRegistry converts registry URLs and aliases to the host form used
// in image references. Docker Hub remains implicit.
func NormalizeRegistry(registry string) string {
	registry = strings.TrimSpace(registry)
	if registry == "" {
		return ""
	}
	if parsed, err := url.Parse(registry); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		registry = parsed.Host
	} else {
		registry = strings.TrimSuffix(registry, "/")
		registry = strings.TrimSuffix(registry, "/v1")
		registry = strings.TrimSuffix(registry, "/v2")
		registry = strings.TrimSuffix(registry, "/")
	}
	if isDockerHub(registry) {
		return "docker.io"
	}
	return strings.ToLower(registry)
}

func sameRegistry(left, right string) bool {
	return NormalizeRegistry(left) == NormalizeRegistry(right)
}

// Resolve combines a repository with its resolved tags. When
// ExpandRepository is enabled, a custom registry is prefixed unless the
// repository already contains it. Docker Hub's legacy v1 URL is not prefixed,
// matching drone-kaniko's behavior.
func Resolve(input Input) (Result, error) {
	if len(input.Direct) > 0 {
		return resolveDirect(input.Direct)
	}

	repository := strings.TrimSpace(input.Repository)
	if err := validateRepository(repository); err != nil {
		return Result{}, err
	}

	if input.ExpandRepository || input.ForceRegistryPrefix {
		registry := normalizeRegistry(input.Registry)
		if input.ForceRegistryPrefix && registry == "" {
			return Result{}, fmt.Errorf("registry is required when registry prefixing is forced")
		}
		var err error
		repository, err = QualifyRepository(input.Registry, repository, true)
		if err != nil {
			return Result{}, err
		}
	}

	if len(input.Tags) == 0 {
		return Result{}, fmt.Errorf("at least one image tag is required")
	}

	tags := make([]string, 0, len(input.Tags))
	destinations := make([]string, 0, len(input.Tags))
	images := make([]Image, 0, len(input.Tags))
	for _, rawTag := range input.Tags {
		tag := strings.TrimSpace(rawTag)
		if !tagPattern.MatchString(tag) {
			return Result{}, fmt.Errorf("invalid image tag %q", rawTag)
		}
		tags = append(tags, tag)
		reference := repository + ":" + tag
		destinations = append(destinations, reference)
		images = append(images, Image{Repository: repository, Tag: tag, Reference: reference})
	}

	return Result{
		Repository:   repository,
		Tags:         tags,
		Destinations: destinations,
		Images:       images,
	}, nil
}

// QualifyRepository prefixes repository with registry when prefix is true.
// It accepts an optional tag on repository so the same function can normalize
// PLUGIN_CACHE_REPO. Docker Hub's legacy/default registry names are left
// unqualified for compatibility with the generic Docker and Kaniko plugins.
func QualifyRepository(registry, repository string, prefix bool) (string, error) {
	repository = strings.TrimSpace(repository)
	if err := validateRepositoryReference(repository); err != nil {
		return "", err
	}
	if !prefix {
		return repository, nil
	}

	registry = normalizeRegistry(registry)
	if registry == "" || isDockerHub(registry) {
		return repository, nil
	}
	if strings.HasPrefix(repository, registry+"/") {
		return repository, nil
	}
	return registry + "/" + repository, nil
}

func resolveDirect(values []string) (Result, error) {
	seen := make(map[string]struct{}, len(values))
	result := Result{
		Destinations: make([]string, 0, len(values)),
		Images:       make([]Image, 0, len(values)),
	}

	for _, raw := range values {
		reference := strings.TrimSpace(raw)
		repository, tag, err := splitReference(reference)
		if err != nil {
			return Result{}, err
		}
		if _, ok := seen[reference]; ok {
			continue
		}
		seen[reference] = struct{}{}
		result.Destinations = append(result.Destinations, reference)
		result.Images = append(result.Images, Image{Repository: repository, Tag: tag, Reference: reference})

		if len(result.Images) == 1 {
			result.Repository = repository
		} else if result.Repository != repository {
			result.Repository = ""
		}
	}

	if len(result.Destinations) == 0 {
		return Result{}, fmt.Errorf("at least one direct destination is required")
	}
	if result.Repository != "" {
		result.Tags = make([]string, 0, len(result.Images))
		for _, image := range result.Images {
			result.Tags = append(result.Tags, image.Tag)
		}
	}
	return result, nil
}

func splitReference(reference string) (string, string, error) {
	if reference == "" {
		return "", "", fmt.Errorf("direct destination must not be empty")
	}
	if strings.Contains(reference, "@") {
		return "", "", fmt.Errorf("direct destination %q must use a tag, not a digest", reference)
	}
	lastSlash := strings.LastIndex(reference, "/")
	lastColon := strings.LastIndex(reference, ":")
	if lastColon <= lastSlash {
		return "", "", fmt.Errorf("direct destination %q must include an explicit tag", reference)
	}
	repository, tag := reference[:lastColon], reference[lastColon+1:]
	if err := validateRepository(repository); err != nil {
		return "", "", fmt.Errorf("invalid direct destination %q: %w", reference, err)
	}
	if !tagPattern.MatchString(tag) {
		return "", "", fmt.Errorf("invalid image tag %q in direct destination %q", tag, reference)
	}
	return repository, tag, nil
}

func validateRepository(repository string) error {
	if err := validateRepositoryReference(repository); err != nil {
		return err
	}

	// A colon after the final slash is an existing tag. A colon before the
	// final slash is a registry port and is valid (for example localhost:5000/app).
	if colon := strings.LastIndex(repository, ":"); colon > strings.LastIndex(repository, "/") {
		return fmt.Errorf("image repository %q must not include a tag", repository)
	}
	return nil
}

func validateRepositoryReference(repository string) error {
	if repository == "" {
		return fmt.Errorf("image repository is required")
	}
	if strings.ContainsAny(repository, " \t\r\n") {
		return fmt.Errorf("image repository %q contains whitespace", repository)
	}
	if strings.Contains(repository, "://") {
		return fmt.Errorf("image repository %q must not include a URL scheme", repository)
	}
	if strings.Contains(repository, "@") {
		return fmt.Errorf("image repository %q must not include a digest", repository)
	}
	if strings.HasPrefix(repository, "/") || strings.HasSuffix(repository, "/") {
		return fmt.Errorf("invalid image repository %q", repository)
	}
	return nil
}

func normalizeRegistry(registry string) string {
	registry = NormalizeRegistry(registry)
	if isDockerHub(registry) {
		return ""
	}
	return registry
}

func isDockerHub(registry string) bool {
	switch strings.ToLower(registry) {
	case "docker.io", "index.docker.io", "registry-1.docker.io", "registry.hub.docker.com":
		return true
	default:
		return false
	}
}
