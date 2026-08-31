// Package archivepush publishes a single-image Docker archive without a
// Docker daemon. It is used for the Kaniko-compatible push-only workflow.
package archivepush

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"

	"github.com/harness-community/drone-kimia/internal/destination"
)

// Options describes one push-only operation. Destinations must already be
// fully resolved image references, including tags. Authentication is read from
// the standard Docker config selected by DOCKER_CONFIG.
type Options struct {
	SourceTarPath      string
	Destinations       []string
	Insecure           bool
	InsecureRegistries []string
	Writer             io.Writer
}

type loadImageFunc func(string) (v1.Image, error)
type pushImageFunc func(context.Context, v1.Image, string, bool) error

type dependencies struct {
	load loadImageFunc
	push pushImageFunc
}

var defaultDependencies = dependencies{
	load: func(source string) (v1.Image, error) {
		return crane.Load(source)
	},
	push: func(ctx context.Context, image v1.Image, target string, insecure bool) error {
		options := []crane.Option{
			crane.WithContext(ctx),
			crane.WithAuthFromKeychain(authn.DefaultKeychain),
		}
		if insecure {
			options = append(options, crane.Insecure)
		}
		return crane.Push(image, target, options...)
	},
}

// Push loads SourceTarPath once and publishes the image to every destination.
// The returned digest is the manifest digest written by crane and is suitable
// for Harness artifact and digest output files.
func Push(ctx context.Context, options Options) (string, error) {
	return push(ctx, options, defaultDependencies)
}

func push(ctx context.Context, options Options, deps dependencies) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("push context is required")
	}
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("push source image archive: %w", err)
	}
	if deps.load == nil || deps.push == nil {
		return "", fmt.Errorf("archive push dependencies are incomplete")
	}

	source := strings.TrimSpace(options.SourceTarPath)
	if source == "" {
		return "", fmt.Errorf("source image archive path is required")
	}
	info, err := os.Stat(source)
	if err != nil {
		return "", fmt.Errorf("stat source image archive %q: %w", source, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("source image archive %q must be a regular file", source)
	}
	targets, err := resolveTargets(options.Destinations, options.Insecure, options.InsecureRegistries)
	if err != nil {
		return "", err
	}

	image, err := deps.load(source)
	if err != nil {
		if strings.Contains(err.Error(), "tarball must contain only a single image") {
			return "", fmt.Errorf("source image archive %q must contain exactly one image: %w", source, err)
		}
		return "", fmt.Errorf("load source image archive %q: %w", source, err)
	}
	digest, err := image.Digest()
	if err != nil {
		return "", fmt.Errorf("calculate source image archive digest: %w", err)
	}

	writer := options.Writer
	if writer == nil {
		writer = io.Discard
	}
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return "", fmt.Errorf("push source image archive to %q: %w", target.reference, err)
		}
		if err := deps.push(ctx, image, target.reference, target.insecure); err != nil {
			return "", fmt.Errorf("push source image archive to %q: %w", target.reference, err)
		}
		fmt.Fprintf(writer, "Successfully pushed image to %s\n", target.reference)
	}

	return digest.String(), nil
}

type pushTarget struct {
	reference string
	insecure  bool
}

func resolveTargets(values []string, insecure bool, insecureRegistries []string) ([]pushTarget, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("at least one image destination is required for push-only")
	}

	insecureHosts := make(map[string]struct{}, len(insecureRegistries))
	for _, value := range insecureRegistries {
		if host := destination.NormalizeRegistry(value); host != "" {
			insecureHosts[host] = struct{}{}
		}
	}

	targets := make([]pushTarget, 0, len(values))
	for _, value := range values {
		reference := strings.TrimSpace(value)
		if reference == "" {
			return nil, fmt.Errorf("image destination must not be empty")
		}
		parsed, err := name.ParseReference(reference)
		if err != nil {
			return nil, fmt.Errorf("invalid image destination %q: %w", reference, err)
		}
		host := destination.NormalizeRegistry(parsed.Context().RegistryStr())
		_, hostInsecure := insecureHosts[host]
		targets = append(targets, pushTarget{
			reference: reference,
			insecure:  insecure || hostInsecure,
		})
	}
	return targets, nil
}
