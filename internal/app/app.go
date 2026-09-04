package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/harness-community/drone-kimia/internal/archivepush"
	"github.com/harness-community/drone-kimia/internal/auth"
	"github.com/harness-community/drone-kimia/internal/config"
	"github.com/harness-community/drone-kimia/internal/destination"
	"github.com/harness-community/drone-kimia/internal/envutil"
	"github.com/harness-community/drone-kimia/internal/kimia"
	"github.com/harness-community/drone-kimia/internal/registrydigest"
	"github.com/harness-community/drone-kimia/internal/result"
	"github.com/harness-community/drone-kimia/internal/workspacecompat"
)

const childShutdownTimeout = 10 * time.Second

var pushImageArchive = archivepush.Push
var resolveRegistryDigests = registrydigest.Resolve

type Streams struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

func Run(ctx context.Context, provider string, streams Streams) error {
	if err := envutil.LoadFile(); err != nil {
		return err
	}
	cfg, err := config.Load(provider)
	if err != nil {
		return err
	}

	if err := os.MkdirAll("/tmp/run", 0o700); err != nil {
		return fmt.Errorf("prepare Kimia runtime directory: %w", err)
	}

	inferredRegistry, err := destination.InferRegistry(cfg.Repo, cfg.Destinations)
	if err != nil {
		return fmt.Errorf("infer destination registry: %w", err)
	}
	cfg.Registry, err = destination.ReconcileRegistry(cfg.Registry, inferredRegistry)
	if err != nil {
		return fmt.Errorf("validate destination registry: %w", err)
	}
	destinationRegistry := cfg.Registry
	willPush := cfg.PushOnly || (!cfg.NoPush && cfg.TarPath == "")
	cacheRegistries := destination.CacheRegistries(cfg.CacheRepo, cfg.ImportCache, cfg.ExportCache)
	authRegistry := destination.NormalizeRegistry(destinationRegistry)
	if authRegistry == "" {
		authRegistry = providerCacheRegistry(auth.Provider(provider), cacheRegistries)
	}
	cacheUsesAuthRegistry := cacheTargetsRegistry(cfg, authRegistry, cacheRegistries)
	cacheAuthOnly := !willPush && destinationRegistry == "" && (cfg.CacheRepo != "" || hasRegistryCache(cfg.ImportCache) || hasRegistryCache(cfg.ExportCache))

	sourceConfigDir := strings.TrimSpace(os.Getenv("DOCKER_CONFIG"))
	privateConfigDir, err := os.MkdirTemp("", "drone-kimia-docker-config-*")
	if err != nil {
		return fmt.Errorf("create private Docker config directory: %w", err)
	}
	defer os.RemoveAll(privateConfigDir)
	restoreDockerConfig := preserveEnvironment("DOCKER_CONFIG")
	defer restoreDockerConfig()

	authResult, err := auth.Prepare(ctx, auth.Options{
		Provider:        auth.Provider(provider),
		Registry:        authRegistry,
		Push:            requiresRegistryAuthentication(cfg, authRegistry, cacheUsesAuthRegistry),
		ConfigDir:       privateConfigDir,
		SourceConfigDir: sourceConfigDir,
	})
	if err != nil {
		return fmt.Errorf("prepare registry authentication: %w", err)
	}
	if authResult.Registry != "" {
		resolvedAuthRegistry := destination.NormalizeRegistry(authResult.Registry)
		if authRegistry != "" && resolvedAuthRegistry != authRegistry {
			return fmt.Errorf("registry authentication resolved host %q, expected %q", resolvedAuthRegistry, authRegistry)
		}
		authRegistry = resolvedAuthRegistry
		if destinationRegistry == "" && !cacheAuthOnly {
			destinationRegistry = authResult.Registry
			cfg.Registry = destinationRegistry
		}
	}
	cacheRegistry := destinationRegistry
	if cacheAuthOnly && authRegistry != "" {
		cacheRegistry = authRegistry
	}

	forceRegistryPrefix := provider != string(auth.ProviderDocker) && destinationRegistry != ""
	resolved, err := destination.Resolve(destination.Input{
		Registry:            destinationRegistry,
		Repository:          cfg.Repo,
		Tags:                cfg.Tags,
		Direct:              cfg.Destinations,
		ExpandRepository:    cfg.ExpandRepo,
		ForceRegistryPrefix: forceRegistryPrefix,
	})
	if err != nil {
		return fmt.Errorf("resolve image destinations: %w", err)
	}
	if cfg.PushOnly {
		cleanup, err := ensureDigestOutput(&cfg)
		if err != nil {
			return err
		}
		defer cleanup()

		digest, err := pushImageArchive(ctx, archivepush.Options{
			SourceTarPath:      cfg.SourceTarPath,
			Destinations:       resolved.Destinations,
			Insecure:           cfg.Insecure,
			InsecureRegistries: cfg.InsecureRegistries,
			Writer:             streams.Stdout,
		})
		if err != nil {
			return fmt.Errorf("push existing image archive: %w", err)
		}
		if cfg.DigestFile != "" {
			if err := result.WriteDigest(cfg.DigestFile, digest); err != nil {
				return fmt.Errorf("write pushed image digest: %w", err)
			}
		}
		writeRequestedResults(cfg, resolved, destinationRegistry, streams.Stderr)
		return nil
	}
	if cfg.CacheRepo != "" {
		cfg.CacheRepo, err = destination.QualifyRepository(
			cacheRegistry,
			cfg.CacheRepo,
			cfg.ExpandRepo || (provider != string(auth.ProviderDocker) && cacheRegistry != ""),
		)
		if err != nil {
			return fmt.Errorf("resolve cache repository: %w", err)
		}
	}

	verifyPushedDigests := shouldResolveRegistryDigests(cfg)
	cleanup := func() {}
	if !verifyPushedDigests {
		cleanup, err = ensureDigestOutput(&cfg)
		if err != nil {
			return err
		}
	}
	defer cleanup()

	workspacePlan, err := workspacecompat.Prepare(workspacecompat.Input{
		Context:    cfg.Context,
		Dockerfile: cfg.Dockerfile,
		TarPath:    cfg.TarPath,
	})
	if err != nil {
		return fmt.Errorf("prepare Kimia workspace compatibility: %w", err)
	}
	defer func() {
		if err := workspacePlan.Cleanup(); err != nil {
			warn(streams.Stderr, "could not clean workspace compatibility paths: %v", err)
		}
	}()
	cfg.Context = workspacePlan.Context
	cfg.Dockerfile = workspacePlan.Dockerfile
	cfg.TarPath = workspacePlan.TarPath

	renderConfig := cfg
	if verifyPushedDigests {
		// Kimia v1.0.26's Buildah push reports its config blob digest. Do not
		// let that value reach user-visible output paths; the wrapper writes
		// verified registry manifest digests after the push succeeds.
		renderConfig.DigestFile = ""
		renderConfig.ImageNameWithDigestFile = ""
	}
	command, err := kimia.Render(renderConfig, resolved.Destinations)
	if err != nil {
		return fmt.Errorf("render Kimia command: %w", err)
	}
	cosignPasswordEnv := ""
	if cfg.Sign {
		cosignPasswordEnv = cfg.CosignPasswordEnv
	}
	if err := execute(ctx, command, streams, cosignPasswordEnv); err != nil {
		if cleanupErr := workspacePlan.Finalize(false); cleanupErr != nil {
			warn(streams.Stderr, "could not clean failed workspace adaptation: %v", cleanupErr)
		}
		return err
	}
	if err := workspacePlan.Finalize(true); err != nil {
		return fmt.Errorf("publish Kimia workspace outputs: %w", err)
	}
	cfg.TarPath = workspacePlan.OriginalTarPath

	if verifyPushedDigests {
		digests, err := resolveRegistryDigests(ctx, registrydigest.Options{
			Destinations:       resolved.Destinations,
			Insecure:           cfg.Insecure,
			InsecureRegistries: cfg.InsecureRegistries,
		})
		if err != nil {
			suppressUnverifiedResults(cfg, streams.Stderr)
			warn(streams.Stderr, "image push succeeded but registry manifest digest verification failed; digest-derived outputs were suppressed: %v", err)
			return nil
		}
		if err := writeVerifiedPushResults(cfg, resolved, destinationRegistry, digests, streams.Stderr); err != nil {
			suppressUnverifiedResults(cfg, streams.Stderr)
			warn(streams.Stderr, "image push succeeded but registry manifest digest verification was incomplete; digest-derived outputs were suppressed: %v", err)
		}
		return nil
	}

	writeRequestedResults(cfg, resolved, destinationRegistry, streams.Stderr)
	return nil
}

// RunWithSignals runs the plugin until the build completes or the process
// receives an interrupt/termination signal.
func RunWithSignals(provider string, streams Streams) error {
	ctx, stop := signalContext()
	defer stop()
	return Run(ctx, provider, streams)
}

// ExitCode preserves Kimia's non-zero exit status when the wrapper reports a
// subprocess failure. Validation and wrapper failures use the conventional
// generic failure code.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && exitError.ExitCode() > 0 {
		return exitError.ExitCode()
	}
	return 1
}

func requiresRegistryAuthentication(cfg config.Config, registry string, cacheUsesRegistry bool) bool {
	if cfg.PushOnly {
		return true
	}
	if !cfg.NoPush && cfg.TarPath == "" {
		return true
	}
	if cacheUsesRegistry {
		return true
	}

	switch auth.Provider(cfg.Provider) {
	case auth.ProviderDocker:
		return envutil.First("PLUGIN_USERNAME", "DOCKER_USERNAME", "PLUGIN_PASSWORD", "DOCKER_PASSWORD", "ACCESS_TOKEN") != ""
	case auth.ProviderGAR:
		return registry != "" || envutil.First(
			"PLUGIN_LOCATION", "PLUGIN_JSON_KEY", "GCR_JSON_KEY", "GOOGLE_CREDENTIALS", "TOKEN",
			"PLUGIN_OIDC_TOKEN_ID", "GOOGLE_APPLICATION_CREDENTIALS",
		) != ""
	case auth.ProviderECR:
		return registry != "" || envutil.First(
			"PLUGIN_ACCESS_KEY", "ECR_ACCESS_KEY", "AWS_ACCESS_KEY_ID",
			"PLUGIN_SECRET_KEY", "ECR_SECRET_KEY", "AWS_SECRET_ACCESS_KEY",
			"PLUGIN_ASSUME_ROLE", "PLUGIN_OIDC_TOKEN_ID", "AWS_PROFILE", "AWS_WEB_IDENTITY_TOKEN_FILE",
			"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI", "AWS_CONTAINER_CREDENTIALS_FULL_URI",
		) != ""
	case auth.ProviderACR:
		return registry != "" || envutil.First(
			"SERVICE_PRINCIPAL_CLIENT_ID", "SERVICE_PRINCIPAL_CLIENT_SECRET",
			"CLIENT_ID", "AZURE_CLIENT_ID", "AZURE_APP_ID", "PLUGIN_CLIENT_ID",
			"CLIENT_SECRET", "PLUGIN_CLIENT_SECRET", "CLIENT_CERTIFICATE", "PLUGIN_CLIENT_CERTIFICATE",
			"PLUGIN_OIDC_TOKEN_ID", "AZURE_FEDERATED_TOKEN_FILE",
		) != ""
	default:
		return false
	}
}

func providerCacheRegistry(provider auth.Provider, registries []string) string {
	match := ""
	for _, registry := range registries {
		if !registryMatchesProvider(provider, registry) {
			continue
		}
		if match != "" && match != registry {
			return ""
		}
		match = registry
	}
	return match
}

func registryMatchesProvider(provider auth.Provider, registry string) bool {
	registry = strings.ToLower(destination.NormalizeRegistry(registry))
	switch provider {
	case auth.ProviderDocker:
		return registry != ""
	case auth.ProviderGAR:
		return strings.HasSuffix(registry, "-docker.pkg.dev")
	case auth.ProviderECR:
		return strings.Contains(registry, ".dkr.ecr.") || strings.Contains(registry, ".dkr.ecr-fips.")
	case auth.ProviderACR:
		return strings.HasSuffix(registry, ".azurecr.io")
	default:
		return false
	}
}

func cacheTargetsRegistry(cfg config.Config, registry string, cacheRegistries []string) bool {
	registry = destination.NormalizeRegistry(registry)
	if registry == "" {
		return false
	}
	if cfg.CacheRepo != "" && destination.RegistryHost(cfg.CacheRepo) == "" {
		return true
	}
	for _, cacheRegistry := range cacheRegistries {
		if destination.NormalizeRegistry(cacheRegistry) == registry {
			return true
		}
	}
	return false
}

func hasRegistryCache(values []string) bool {
	for _, value := range values {
		normalized := strings.ToLower(strings.TrimSpace(value))
		if !strings.HasPrefix(normalized, "type=") || strings.HasPrefix(normalized, "type=registry,") || normalized == "type=registry" {
			return true
		}
	}
	return false
}

func ensureDigestOutput(cfg *config.Config) (func(), error) {
	if cfg.DigestFile != "" || (cfg.ImageNameWithDigestFile == "" && cfg.ArtifactFile == "" && cfg.DroneOutput == "") {
		return func() {}, nil
	}
	directory, err := os.MkdirTemp("", "drone-kimia-result-*")
	if err != nil {
		return nil, fmt.Errorf("create temporary Kimia result directory: %w", err)
	}
	cfg.DigestFile = filepath.Join(directory, "digest")
	return func() { _ = os.RemoveAll(directory) }, nil
}

func execute(ctx context.Context, command kimia.Command, streams Streams, cosignPasswordEnv string) error {
	child := exec.Command(command.Path, command.Args...)
	child.Stdin = streams.Stdin
	child.Stdout = streams.Stdout
	child.Stderr = streams.Stderr
	child.Env = kimiaEnvironment(cosignPasswordEnv)
	configureChild(child)
	if err := child.Start(); err != nil {
		return fmt.Errorf("start Kimia: %w", err)
	}

	done := make(chan error, 1)
	go func() { done <- child.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("Kimia build failed: %w", err)
		}
		return nil
	case <-ctx.Done():
		_ = terminateChild(child.Process)
		timer := time.NewTimer(childShutdownTimeout)
		defer timer.Stop()
		select {
		case <-done:
			// Kimia may exit before its builder descendants. Kill any processes
			// that remain in the isolated process group.
			_ = killChild(child.Process)
		case <-timer.C:
			_ = killChild(child.Process)
			<-done
		}
		return fmt.Errorf("Kimia build interrupted: %w", ctx.Err())
	}
}

func shouldResolveRegistryDigests(cfg config.Config) bool {
	if cfg.PushOnly || cfg.NoPush || cfg.TarPath != "" {
		return false
	}
	return cfg.DigestFile != "" || cfg.ImageNameWithDigestFile != "" || cfg.ArtifactFile != "" || cfg.DroneOutput != ""
}

func writeVerifiedPushResults(cfg config.Config, resolved destination.Result, registryURL string, digests map[string]string, stderr io.Writer) error {
	if len(resolved.Destinations) == 0 || len(resolved.Images) != len(resolved.Destinations) {
		return fmt.Errorf("resolved image destinations are incomplete")
	}
	for _, imageDestination := range resolved.Destinations {
		if strings.TrimSpace(digests[imageDestination]) == "" {
			return fmt.Errorf("verified manifest digest for %q is missing", imageDestination)
		}
	}

	firstDestination := resolved.Destinations[0]
	firstDigest := strings.TrimSpace(digests[firstDestination])
	if cfg.DigestFile != "" {
		if err := result.WriteDigest(cfg.DigestFile, firstDigest); err != nil {
			warn(stderr, "could not write verified digest file: %v", err)
			removeOutput(cfg.DigestFile, "digest", stderr)
		}
	}
	if cfg.ImageNameWithDigestFile != "" {
		if err := result.WriteImageNameWithDigest(
			cfg.ImageNameWithDigestFile,
			resolved.Images[0].Repository,
			firstDigest,
		); err != nil {
			warn(stderr, "could not write verified image name with digest file: %v", err)
			removeOutput(cfg.ImageNameWithDigestFile, "image name with digest", stderr)
		}
	}
	if cfg.ArtifactFile != "" {
		if err := result.WriteArtifactWithDigests(
			cfg.ArtifactFile,
			result.RegistryType(cfg.Provider),
			artifactRegistryURL(cfg.Provider, registryURL),
			resolved.Destinations,
			digests,
		); err != nil {
			warn(stderr, "could not write plugin artifact: %v", err)
			removeOutput(cfg.ArtifactFile, "plugin artifact", stderr)
		}
	}
	if cfg.DroneOutput != "" {
		if err := result.WriteDroneOutput(cfg.DroneOutput, firstDigest, cfg.TarPath); err != nil {
			warn(stderr, "could not write DRONE_OUTPUT: %v", err)
			removeOutput(cfg.DroneOutput, "DRONE_OUTPUT", stderr)
		}
	}
	return nil
}

func suppressUnverifiedResults(cfg config.Config, stderr io.Writer) {
	removeOutput(cfg.DigestFile, "digest", stderr)
	removeOutput(cfg.ImageNameWithDigestFile, "image name with digest", stderr)
	removeOutput(cfg.ArtifactFile, "plugin artifact", stderr)
	removeOutput(cfg.DroneOutput, "DRONE_OUTPUT", stderr)
}

func removeOutput(path, description string, stderr io.Writer) {
	if strings.TrimSpace(path) == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		warn(stderr, "could not remove unverified %s output %q: %v", description, path, err)
	}
}

func kimiaEnvironment(cosignPasswordEnv string) []string {
	allowedSecret := strings.TrimSpace(cosignPasswordEnv)
	result := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key != allowedSecret && connectorSecretEnvironment(key) {
			continue
		}
		result = append(result, entry)
	}
	return result
}

func connectorSecretEnvironment(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	if strings.HasPrefix(upper, "PLUGIN_") || strings.HasPrefix(upper, "AWS_") || strings.HasPrefix(upper, "ECR_") || strings.HasPrefix(upper, "AZURE_") || strings.HasPrefix(upper, "SERVICE_PRINCIPAL_") {
		return true
	}
	switch upper {
	case "ACCESS_TOKEN", "TOKEN", "GCR_JSON_KEY", "GOOGLE_CREDENTIALS", "DOCKER_AUTH_CONFIG", "DOCKER_PLUGIN_CONFIG",
		"DOCKER_USERNAME", "DOCKER_PASSWORD", "DOCKER_REGISTRY",
		"DOCKER_BASE_IMAGE_USERNAME", "DOCKER_BASE_IMAGE_PASSWORD", "DOCKER_BASE_IMAGE_REGISTRY",
		"CLIENT_ID", "CLIENT_SECRET", "CLIENT_CERTIFICATE", "TENANT_ID", "SUBSCRIPTION_ID":
		return true
	default:
		return false
	}
}

func preserveEnvironment(key string) func() {
	value, set := os.LookupEnv(key)
	return func() {
		if set {
			_ = os.Setenv(key, value)
		} else {
			_ = os.Unsetenv(key)
		}
	}
}

func writeRequestedResults(cfg config.Config, resolved destination.Result, registryURL string, stderr io.Writer) {
	if cfg.DigestFile == "" && cfg.ImageNameWithDigestFile == "" && cfg.ArtifactFile == "" && cfg.DroneOutput == "" {
		return
	}
	digest, digestErr := result.ReadDigest(cfg.DigestFile)
	if digestErr != nil {
		warn(stderr, "could not read Kimia digest output: %v", digestErr)
	}
	if cfg.DigestFile != "" && digestErr == nil {
		if err := result.WriteDigest(cfg.DigestFile, digest); err != nil {
			warn(stderr, "could not canonicalize Kimia digest output: %v", err)
			removeOutput(cfg.DigestFile, "digest", stderr)
		}
	}
	if cfg.ImageNameWithDigestFile != "" {
		if digestErr != nil || len(resolved.Images) == 0 {
			warn(stderr, "could not write image name with digest without a valid image digest")
			removeOutput(cfg.ImageNameWithDigestFile, "image name with digest", stderr)
		} else if err := result.WriteImageNameWithDigest(
			cfg.ImageNameWithDigestFile,
			resolved.Images[0].Repository,
			digest,
		); err != nil {
			warn(stderr, "could not write image name with digest: %v", err)
			removeOutput(cfg.ImageNameWithDigestFile, "image name with digest", stderr)
		}
	}
	registryURL = artifactRegistryURL(cfg.Provider, registryURL)
	if cfg.ArtifactFile != "" {
		if digestErr != nil {
			warn(stderr, "could not write plugin artifact without an image digest")
		} else if err := result.WriteArtifact(
			cfg.ArtifactFile,
			result.RegistryType(cfg.Provider),
			registryURL,
			digest,
			resolved.Destinations,
		); err != nil {
			warn(stderr, "could not write plugin artifact: %v", err)
		}
	}
	if cfg.DroneOutput != "" {
		if err := result.WriteDroneOutput(cfg.DroneOutput, digest, cfg.TarPath); err != nil {
			warn(stderr, "could not write DRONE_OUTPUT: %v", err)
		}
	}
}

func artifactRegistryURL(provider, registryURL string) string {
	if provider == string(auth.ProviderDocker) && (registryURL == "" || destination.NormalizeRegistry(registryURL) == "docker.io") {
		return "https://index.docker.io/v1/"
	}
	return registryURL
}

func warn(writer io.Writer, format string, values ...any) {
	if writer == nil {
		return
	}
	fmt.Fprintf(writer, "drone-kimia: warning: "+format+"\n", values...)
}
