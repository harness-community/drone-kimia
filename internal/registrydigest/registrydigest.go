// Package registrydigest resolves the manifest digests stored by a registry
// after Kimia has pushed an image.
package registrydigest

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/harness-community/drone-kimia/internal/destination"
)

// Options describes the pushed destinations to verify. Authentication is read
// from the standard Docker config selected by DOCKER_CONFIG.
type Options struct {
	Destinations       []string
	Insecure           bool
	InsecureRegistries []string
}

type headFunc func(name.Reference, ...remote.Option) (*v1.Descriptor, error)
type getFunc func(name.Reference, ...remote.Option) (*remote.Descriptor, error)

type dependencies struct {
	head      headFunc
	get       getFunc
	transport http.RoundTripper
}

var defaultDependencies = dependencies{
	head:      remote.Head,
	get:       remote.Get,
	transport: remote.DefaultTransport,
}

// Resolve returns the registry manifest digest for each destination. A HEAD
// request is preferred, with GET as a compatibility fallback for registries
// that do not implement manifest HEAD correctly.
func Resolve(ctx context.Context, options Options) (map[string]string, error) {
	return resolve(ctx, options, defaultDependencies)
}

func resolve(ctx context.Context, options Options, deps dependencies) (map[string]string, error) {
	if ctx == nil {
		return nil, fmt.Errorf("registry digest context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("resolve registry manifest digests: %w", err)
	}
	if deps.head == nil || deps.get == nil {
		return nil, fmt.Errorf("registry digest dependencies are incomplete")
	}

	targets, err := resolveTargets(options)
	if err != nil {
		return nil, err
	}
	transport := deps.transport
	if transport == nil {
		transport = remote.DefaultTransport
	}
	remoteOptions := []remote.Option{
		remote.WithContext(ctx),
		remote.WithAuthFromKeychain(authn.DefaultKeychain),
		remote.WithTransport(transport),
	}
	var insecureTransport http.RoundTripper
	digests := make(map[string]string, len(targets))
	for _, target := range targets {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("resolve registry manifest digest for %q: %w", target.destination, err)
		}

		var digest string
		var err error
		if !target.insecure {
			digest, err = lookup(target.secureReference, remoteOptions, deps)
		} else {
			if insecureTransport == nil {
				insecureTransport, err = transportWithoutTLSVerification(transport)
				if err != nil {
					return nil, fmt.Errorf("configure insecure registry transport: %w", err)
				}
			}
			httpsOptions := append([]remote.Option{}, remoteOptions...)
			httpsOptions = append(httpsOptions, remote.WithTransport(insecureTransport))
			digest, err = lookup(target.secureReference, httpsOptions, deps)
			if err != nil {
				httpsErr := err
				digest, err = lookup(target.insecureReference, remoteOptions, deps)
				if err != nil {
					err = fmt.Errorf("insecure HTTPS lookup failed: %v; plain HTTP fallback failed: %w", httpsErr, err)
				}
			}
		}
		if err != nil {
			return nil, fmt.Errorf("resolve registry manifest digest for %q: %w", target.destination, err)
		}
		digests[target.destination] = digest
	}
	return digests, nil
}

type target struct {
	destination       string
	secureReference   name.Reference
	insecureReference name.Reference
	insecure          bool
}

func resolveTargets(options Options) ([]target, error) {
	if len(options.Destinations) == 0 {
		return nil, fmt.Errorf("at least one pushed image destination is required")
	}

	insecureHosts := make(map[string]struct{}, len(options.InsecureRegistries))
	for _, value := range options.InsecureRegistries {
		if host := destination.NormalizeRegistry(value); host != "" {
			insecureHosts[host] = struct{}{}
		}
	}

	targets := make([]target, 0, len(options.Destinations))
	for _, value := range options.Destinations {
		referenceText := strings.TrimSpace(value)
		if referenceText == "" {
			return nil, fmt.Errorf("pushed image destination must not be empty")
		}
		parsed, err := name.ParseReference(referenceText, name.StrictValidation)
		if err != nil {
			return nil, fmt.Errorf("invalid pushed image destination %q: %w", referenceText, err)
		}
		host := destination.NormalizeRegistry(parsed.Context().RegistryStr())
		_, hostInsecure := insecureHosts[host]
		isInsecure := options.Insecure || hostInsecure
		target := target{
			destination:     referenceText,
			secureReference: parsed,
			insecure:        isInsecure,
		}
		if isInsecure {
			target.insecureReference, err = name.ParseReference(referenceText, name.StrictValidation, name.Insecure)
			if err != nil {
				return nil, fmt.Errorf("invalid insecure pushed image destination %q: %w", referenceText, err)
			}
		}
		targets = append(targets, target)
	}
	return targets, nil
}

func transportWithoutTLSVerification(base http.RoundTripper) (http.RoundTripper, error) {
	transport, ok := base.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("registry transport has unsupported type %T", base)
	}
	clone := transport.Clone()
	if clone.TLSClientConfig == nil {
		clone.TLSClientConfig = &tls.Config{}
	} else {
		clone.TLSClientConfig = clone.TLSClientConfig.Clone()
	}
	// This transport is used only when the user explicitly disables registry
	// TLS verification through PLUGIN_INSECURE or an insecure-registry entry.
	clone.TLSClientConfig.InsecureSkipVerify = true // #nosec G402 -- explicit user-selected insecure registry behavior
	return clone, nil
}

func lookup(reference name.Reference, options []remote.Option, deps dependencies) (string, error) {
	headDescriptor, headErr := deps.head(reference, options...)
	if headErr == nil {
		if digest, err := descriptorDigest(headDescriptor); err == nil {
			return digest, nil
		} else {
			headErr = err
		}
	}

	getDescriptor, getErr := deps.get(reference, options...)
	if getErr == nil {
		if getDescriptor == nil {
			getErr = fmt.Errorf("GET returned an empty descriptor")
		} else if digest, err := descriptorDigest(&getDescriptor.Descriptor); err == nil {
			return digest, nil
		} else {
			getErr = err
		}
	}
	return "", fmt.Errorf("manifest HEAD failed: %v; manifest GET failed: %w", headErr, getErr)
}

func descriptorDigest(descriptor *v1.Descriptor) (string, error) {
	if descriptor == nil {
		return "", fmt.Errorf("registry returned an empty descriptor")
	}
	digest := descriptor.Digest.String()
	if _, err := v1.NewHash(digest); err != nil {
		return "", fmt.Errorf("registry returned invalid manifest digest %q: %w", digest, err)
	}
	return digest, nil
}
