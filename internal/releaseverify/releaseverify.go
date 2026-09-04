// Package releaseverify validates architecture-specific plugin images in a
// registry before the release pipeline publishes multi-architecture manifests.
package releaseverify

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

const (
	// DefaultRepositoryPrefix is the Docker Hub namespace used by releases.
	DefaultRepositoryPrefix = "docker.io/plugins"
	verifiedImageCount      = 8
)

var (
	providers = []providerSpec{
		{name: "docker", repository: "kimia", title: "drone-kimia"},
		{name: "gar", repository: "kimia-gar", title: "drone-kimia-gar"},
		{name: "ecr", repository: "kimia-ecr", title: "drone-kimia-ecr"},
		{name: "acr", repository: "kimia-acr", title: "drone-kimia-acr"},
	}
	architectures = []string{"amd64", "arm64"}
)

type providerSpec struct {
	name       string
	repository string
	title      string
}

// Options describes the registry artifacts and credentials to verify.
// Username and Password are used only to construct the registry authenticator;
// they are never included in status or error output.
type Options struct {
	RepositoryPrefix string
	ReleaseTag       string
	Revision         string
	Username         string
	Password         string
	Insecure         bool
	Writer           io.Writer
}

type verifiedImage struct {
	reference    string
	provider     string
	architecture string
	digest       string
}

// Verify pulls and validates all four provider images for amd64 and arm64.
// It returns only after every image contract and digest uniqueness check passes.
func Verify(ctx context.Context, options Options) error {
	if ctx == nil {
		return fmt.Errorf("verification context is required")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("verify release images: %w", err)
	}

	prefix := strings.TrimSuffix(strings.TrimSpace(options.RepositoryPrefix), "/")
	if prefix == "" {
		return fmt.Errorf("repository prefix is required")
	}
	revision := strings.TrimSpace(options.Revision)
	if revision == "" || strings.EqualFold(revision, "null") || revision == "unknown" {
		return fmt.Errorf("release revision is required")
	}
	username := strings.TrimSpace(options.Username)
	if username == "" {
		return fmt.Errorf("registry username is required")
	}
	if options.Password == "" {
		return fmt.Errorf("registry password is required")
	}

	releaseTag, err := normalizeReleaseTag(options.ReleaseTag)
	if err != nil {
		return err
	}
	authenticator := authn.FromConfig(authn.AuthConfig{
		Username: username,
		Password: options.Password,
	})
	remoteOptions := []remote.Option{
		remote.WithContext(ctx),
		remote.WithAuth(authenticator),
	}
	parseOptions := []name.Option{name.StrictValidation}
	if options.Insecure {
		parseOptions = append(parseOptions, name.Insecure)
	}

	verified := make([]verifiedImage, 0, verifiedImageCount)
	for _, provider := range providers {
		for _, architecture := range architectures {
			reference := fmt.Sprintf(
				"%s/%s:%s",
				prefix,
				provider.repository,
				architectureTag(releaseTag, architecture),
			)
			parsed, parseErr := name.ParseReference(reference, parseOptions...)
			if parseErr != nil {
				return fmt.Errorf("parse release image %q: %w", reference, parseErr)
			}
			image, pullErr := remote.Image(parsed, remoteOptions...)
			if pullErr != nil {
				return fmt.Errorf("pull release image %q: %w", reference, pullErr)
			}
			config, configErr := image.ConfigFile()
			if configErr != nil {
				return fmt.Errorf("read release image config %q: %w", reference, configErr)
			}
			if validationErr := validateConfig(provider, architecture, revision, config); validationErr != nil {
				return fmt.Errorf("validate release image %q: %w", reference, validationErr)
			}
			if compatibilityErr := verifyCompatibilityEntrypoint(image, provider); compatibilityErr != nil {
				return fmt.Errorf("validate release image %q: %w", reference, compatibilityErr)
			}
			digest, digestErr := image.Digest()
			if digestErr != nil {
				return fmt.Errorf("read release image digest %q: %w", reference, digestErr)
			}
			verified = append(verified, verifiedImage{
				reference:    reference,
				provider:     provider.name,
				architecture: architecture,
				digest:       digest.String(),
			})
		}
	}

	if err := verifyUniqueDigests(verified); err != nil {
		return err
	}
	writer := options.Writer
	if writer == nil {
		writer = io.Discard
	}
	for _, result := range verified {
		fmt.Fprintf(writer, "verified %s@%s\n", result.reference, result.digest)
	}
	fmt.Fprintf(writer, "verified %d architecture images with unique provider digests\n", len(verified))
	return nil
}

func normalizeReleaseTag(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "null") {
		return "", nil
	}
	value = strings.TrimPrefix(value, "v")
	if value == "" {
		return "", fmt.Errorf("release tag must not be only a v prefix")
	}
	return value, nil
}

func architectureTag(releaseTag, architecture string) string {
	if releaseTag == "" {
		return "linux-" + architecture
	}
	return releaseTag + "-linux-" + architecture
}

func validateConfig(provider providerSpec, architecture, revision string, config *v1.ConfigFile) error {
	if config == nil {
		return fmt.Errorf("image config is empty")
	}
	if config.Architecture != architecture {
		return fmt.Errorf("architecture is %q, expected %q", config.Architecture, architecture)
	}
	if config.OS != "linux" {
		return fmt.Errorf("operating system is %q, expected %q", config.OS, "linux")
	}
	expectedEntrypoint := "/usr/local/bin/kimia-" + provider.name
	if len(config.Config.Entrypoint) != 1 || config.Config.Entrypoint[0] != expectedEntrypoint {
		return fmt.Errorf("entrypoint is %q, expected [%q]", config.Config.Entrypoint, expectedEntrypoint)
	}
	title := config.Config.Labels["org.opencontainers.image.title"]
	if title != provider.title {
		return fmt.Errorf("title is %q, expected %q", title, provider.title)
	}
	actualRevision := config.Config.Labels["org.opencontainers.image.revision"]
	if actualRevision != revision {
		return fmt.Errorf("revision is %q, expected %q", actualRevision, revision)
	}
	return nil
}

type filesystemEntry struct {
	found    bool
	typeflag byte
	linkname string
	mode     int64
}

func verifyCompatibilityEntrypoint(image v1.Image, provider providerSpec) error {
	binaryPath := "usr/local/bin/kimia-" + provider.name
	aliasPath := "kaniko/kaniko-" + provider.name
	entries, err := finalFilesystemEntries(image, []string{binaryPath, aliasPath})
	if err != nil {
		return fmt.Errorf("inspect compatibility entrypoint filesystem: %w", err)
	}

	binary := entries[binaryPath]
	if !binary.found {
		return fmt.Errorf("plugin binary /%s is missing", binaryPath)
	}
	if binary.typeflag != tar.TypeReg && binary.typeflag != tar.TypeRegA {
		return fmt.Errorf("plugin binary /%s is not a regular file", binaryPath)
	}
	if binary.mode&0o111 == 0 {
		return fmt.Errorf("plugin binary /%s is not executable", binaryPath)
	}

	alias := entries[aliasPath]
	if !alias.found {
		return fmt.Errorf("Harness compatibility entrypoint /%s is missing", aliasPath)
	}
	switch alias.typeflag {
	case tar.TypeSymlink:
		resolved := resolveSymlink(aliasPath, alias.linkname)
		expected := "/" + binaryPath
		if resolved != expected {
			return fmt.Errorf(
				"Harness compatibility entrypoint /%s resolves to %q, expected %q",
				aliasPath,
				resolved,
				expected,
			)
		}
	case tar.TypeReg, tar.TypeRegA:
		if alias.mode&0o111 == 0 {
			return fmt.Errorf("Harness compatibility entrypoint /%s is not executable", aliasPath)
		}
	default:
		return fmt.Errorf("Harness compatibility entrypoint /%s is not a symlink or executable file", aliasPath)
	}
	return nil
}

func finalFilesystemEntries(image v1.Image, targets []string) (map[string]filesystemEntry, error) {
	layers, err := image.Layers()
	if err != nil {
		return nil, fmt.Errorf("list image layers: %w", err)
	}
	entries := make(map[string]filesystemEntry, len(targets))
	unresolved := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		unresolved[path.Clean(strings.TrimPrefix(target, "/"))] = struct{}{}
	}

	for index := len(layers) - 1; index >= 0 && len(unresolved) > 0; index-- {
		additions, deletions, scanErr := scanLayer(layers[index], unresolved)
		if scanErr != nil {
			return nil, fmt.Errorf("scan layer %d: %w", index, scanErr)
		}
		for target := range unresolved {
			if entry, ok := additions[target]; ok {
				entries[target] = entry
				delete(unresolved, target)
				continue
			}
			if deletions[target] {
				entries[target] = filesystemEntry{}
				delete(unresolved, target)
			}
		}
	}
	for target := range unresolved {
		entries[target] = filesystemEntry{}
	}
	return entries, nil
}

func scanLayer(layer v1.Layer, targets map[string]struct{}) (map[string]filesystemEntry, map[string]bool, error) {
	reader, err := layer.Uncompressed()
	if err != nil {
		return nil, nil, fmt.Errorf("open layer: %w", err)
	}
	defer reader.Close()

	additions := make(map[string]filesystemEntry, len(targets))
	deletions := make(map[string]bool, len(targets))
	tarReader := tar.NewReader(reader)
	for {
		header, nextErr := tarReader.Next()
		if nextErr == io.EOF {
			break
		}
		if nextErr != nil {
			return nil, nil, fmt.Errorf("read layer tar: %w", nextErr)
		}
		entryPath := path.Clean(strings.TrimPrefix(header.Name, "./"))
		if _, ok := targets[entryPath]; ok {
			additions[entryPath] = filesystemEntry{
				found:    true,
				typeflag: header.Typeflag,
				linkname: header.Linkname,
				mode:     header.Mode,
			}
		}
		for target := range targets {
			if whiteoutRemoves(entryPath, target) {
				deletions[target] = true
			}
		}
	}
	return additions, deletions, nil
}

func whiteoutRemoves(entryPath, target string) bool {
	base := path.Base(entryPath)
	directory := path.Dir(entryPath)
	if base == ".wh..wh..opq" {
		return directory == "." || target == directory || strings.HasPrefix(target, directory+"/")
	}
	if !strings.HasPrefix(base, ".wh.") {
		return false
	}
	removed := path.Join(directory, strings.TrimPrefix(base, ".wh."))
	return target == removed || strings.HasPrefix(target, removed+"/")
}

func resolveSymlink(aliasPath, linkname string) string {
	if path.IsAbs(linkname) {
		return path.Clean(linkname)
	}
	return path.Clean(path.Join("/", path.Dir(aliasPath), linkname))
}

func verifyUniqueDigests(images []verifiedImage) error {
	seen := make(map[string]verifiedImage, len(images))
	for _, image := range images {
		if previous, ok := seen[image.digest]; ok {
			return fmt.Errorf(
				"release image %q (%s/%s) reuses digest %s from %q (%s/%s)",
				image.reference,
				image.provider,
				image.architecture,
				image.digest,
				previous.reference,
				previous.provider,
				previous.architecture,
			)
		}
		seen[image.digest] = image
	}
	return nil
}
