// Package kimia renders the normalized plugin configuration as a Kimia CLI
// invocation. It intentionally emits only options that affect Kimia's
// Buildah backend in v1.0.26.
package kimia

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/harness-community/drone-kimia/internal/config"
)

// Command is a shell-free Kimia invocation suitable for exec.Command.
type Command struct {
	Path string
	Args []string
}

// Render builds a deterministic Kimia v1.0.26 Buildah command. Destinations
// should come from destination.Resolve so the same resolved values can be used
// for artifact metadata.
func Render(cfg config.Config, destinations []string) (Command, error) {
	args, err := Arguments(cfg, destinations)
	if err != nil {
		return Command{}, err
	}
	if strings.TrimSpace(cfg.KimiaPath) == "" {
		return Command{}, fmt.Errorf("Kimia executable path is required")
	}
	return Command{Path: cfg.KimiaPath, Args: args}, nil
}

// Arguments returns the argv following the Kimia executable.
func Arguments(cfg config.Config, destinations []string) ([]string, error) {
	if err := validateBuildahConfig(cfg, destinations); err != nil {
		return nil, err
	}

	cacheImports, cacheExports, err := buildahCacheRepositories(cfg)
	if err != nil {
		return nil, err
	}

	args := []string{
		"--dockerfile=" + cfg.Dockerfile,
		"--context=" + cfg.Context,
	}
	if cfg.ContextSubPath != "" {
		args = append(args, "--context-sub-path="+cfg.ContextSubPath)
	}
	for _, value := range destinations {
		args = append(args, "--destination="+value)
	}

	for _, value := range cfg.BuildArgs {
		args = append(args, "--build-arg="+value)
	}
	for _, value := range cfg.Labels {
		args = append(args, "--label="+value)
	}
	if cfg.Target != "" {
		args = append(args, "--target="+cfg.Target)
	}
	if cfg.Platform != "" {
		args = append(args, "--custom-platform="+cfg.Platform)
	}
	if cfg.StorageDriver != "" {
		args = append(args, "--storage-driver="+cfg.StorageDriver)
	}

	cacheEnabled := cfg.EnableCache || len(cacheImports) > 0 || len(cacheExports) > 0
	args = append(args, "--cache="+strconv.FormatBool(cacheEnabled))
	for _, repository := range cacheImports {
		args = append(args, "--buildah-opt=--cache-from "+repository)
	}
	for _, repository := range cacheExports {
		args = append(args, "--buildah-opt=--cache-to "+repository)
	}
	for _, value := range cfg.BuildahOpts {
		args = append(args, "--buildah-opt="+strings.TrimSpace(value))
	}

	if cfg.NoPush {
		args = append(args, "--no-push")
	}
	if cfg.TarPath != "" {
		args = append(args, "--tar-path="+cfg.TarPath)
	}
	if cfg.DigestFile != "" {
		args = append(args, "--digest-file="+cfg.DigestFile)
	}
	if cfg.ImageNameWithDigestFile != "" {
		args = append(args, "--image-name-with-digest-file="+cfg.ImageNameWithDigestFile)
	}

	if cfg.Insecure {
		args = append(args, "--insecure")
	}
	if cfg.InsecurePull {
		args = append(args, "--insecure-pull")
	}
	for _, value := range cfg.InsecureRegistries {
		args = append(args, "--insecure-registry="+value)
	}
	if cfg.ImageDownloadRetry > 0 {
		args = append(args, "--image-download-retry="+strconv.Itoa(cfg.ImageDownloadRetry))
	}
	if cfg.PushRetry > 0 {
		args = append(args, "--push-retry="+strconv.Itoa(cfg.PushRetry))
	}

	if cfg.Verbosity != "" {
		args = append(args, "--verbosity="+cfg.Verbosity)
	}
	if cfg.LogTimestamp {
		args = append(args, "--log-timestamp")
	}
	if cfg.Reproducible {
		args = append(args, "--reproducible")
	}
	if cfg.Timestamp != "" {
		args = append(args, "--timestamp="+cfg.Timestamp)
	}

	if cfg.GitBranch != "" {
		args = append(args, "--git-branch="+cfg.GitBranch)
	}
	if cfg.GitRevision != "" {
		args = append(args, "--git-revision="+cfg.GitRevision)
	}
	if cfg.GitTokenFile != "" {
		args = append(args, "--git-token-file="+cfg.GitTokenFile)
	}
	if cfg.GitTokenUser != "" {
		args = append(args, "--git-token-user="+cfg.GitTokenUser)
	}

	return args, nil
}

func buildahCacheRepositories(cfg config.Config) ([]string, []string, error) {
	imports := append([]string(nil), cfg.ImportCache...)
	exports := append([]string(nil), cfg.ExportCache...)
	if strings.TrimSpace(cfg.CacheRepo) != "" {
		imports = append(imports, cfg.CacheRepo)
		exports = append(exports, cfg.CacheRepo)
	}

	imports, err := normalizeBuildahCacheList(imports, "import")
	if err != nil {
		return nil, nil, err
	}
	exports, err = normalizeBuildahCacheList(exports, "export")
	if err != nil {
		return nil, nil, err
	}
	return imports, exports, nil
}

func normalizeBuildahCacheList(values []string, direction string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		repository, err := normalizeBuildahCache(value, direction)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[repository]; exists {
			continue
		}
		seen[repository] = struct{}{}
		result = append(result, repository)
	}
	return result, nil
}

func normalizeBuildahCache(value, direction string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("cache %s entry must not be empty", direction)
	}
	if !strings.ContainsAny(value, "=,") {
		if err := validateCacheRepository(value); err != nil {
			return "", fmt.Errorf("invalid cache %s repository %q: %w", direction, value, err)
		}
		return value, nil
	}

	attributes := make(map[string]string)
	for _, rawAttribute := range strings.Split(value, ",") {
		attribute := strings.TrimSpace(rawAttribute)
		key, attributeValue, found := strings.Cut(attribute, "=")
		key = strings.ToLower(strings.TrimSpace(key))
		attributeValue = strings.TrimSpace(attributeValue)
		if !found || key == "" || attributeValue == "" {
			return "", fmt.Errorf("malformed cache %s specification %q", direction, value)
		}
		if _, duplicate := attributes[key]; duplicate {
			return "", fmt.Errorf("cache %s specification %q repeats attribute %q", direction, value, key)
		}
		attributes[key] = attributeValue
	}

	cacheType, ok := attributes["type"]
	if !ok {
		return "", fmt.Errorf("cache %s specification %q is missing type=registry", direction, value)
	}
	if !strings.EqualFold(cacheType, "registry") {
		return "", fmt.Errorf("cache %s type %q is not supported by Kimia's Buildah backend; only registry caches are supported", direction, cacheType)
	}
	for key := range attributes {
		switch key {
		case "type", "ref", "mode":
		default:
			return "", fmt.Errorf("cache %s specification %q uses unsupported attribute %q", direction, value, key)
		}
	}
	repository, ok := attributes["ref"]
	if !ok {
		return "", fmt.Errorf("cache %s specification %q is missing ref", direction, value)
	}
	if mode, ok := attributes["mode"]; ok {
		if mode != "min" && mode != "max" {
			return "", fmt.Errorf("cache %s specification %q has unsupported mode %q; only min or max is recognized", direction, value, mode)
		}
		if direction == "export" && mode == "min" {
			return "", fmt.Errorf("cache export specification %q requests mode=min, which has no Buildah equivalent; use mode=max or omit mode", value)
		}
	}
	if err := validateCacheRepository(repository); err != nil {
		return "", fmt.Errorf("invalid cache %s repository %q: %w", direction, repository, err)
	}
	return repository, nil
}

func validateCacheRepository(value string) error {
	if strings.ContainsAny(value, " \t\r\n") {
		return fmt.Errorf("repository contains whitespace")
	}
	if strings.Contains(value, "://") {
		return fmt.Errorf("repository must not include a URL scheme")
	}
	if strings.HasPrefix(value, "/") || strings.HasSuffix(value, "/") {
		return fmt.Errorf("repository must not begin or end with a slash")
	}
	return nil
}

func validateBuildahConfig(cfg config.Config, destinations []string) error {
	if cfg.Dockerfile == "" {
		return fmt.Errorf("Dockerfile path is required")
	}
	if cfg.Context == "" {
		return fmt.Errorf("build context is required")
	}
	if len(destinations) == 0 {
		return fmt.Errorf("at least one Kimia destination is required")
	}
	for _, value := range destinations {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("Kimia destination must not be empty")
		}
	}
	for _, value := range cfg.BuildArgs {
		key := value
		if index := strings.IndexByte(value, '='); index >= 0 {
			key = value[:index]
		}
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("invalid empty build argument key in %q", value)
		}
	}
	for _, value := range cfg.Labels {
		if index := strings.IndexByte(value, '='); index <= 0 {
			return fmt.Errorf("label %q must use key=value format", value)
		}
	}

	if cfg.DisableCache && (cfg.EnableCache || cfg.CacheRepo != "" || len(cfg.ImportCache) > 0 || len(cfg.ExportCache) > 0) {
		return fmt.Errorf("cache is disabled but cache sources or exports were configured")
	}
	if cfg.CacheDir != "" {
		return fmt.Errorf("PLUGIN_CACHE_DIR is not supported by Kimia v1.0.26's Buildah backend")
	}
	switch cfg.StorageDriver {
	case "", "vfs", "overlay":
	default:
		return fmt.Errorf("PLUGIN_STORAGE_DRIVER must be vfs or overlay; got %q", cfg.StorageDriver)
	}
	if cfg.ImageDownloadRetry < 0 {
		return fmt.Errorf("PLUGIN_IMAGE_DOWNLOAD_RETRY must be nonnegative")
	}
	if cfg.PushRetry < 0 {
		return fmt.Errorf("PLUGIN_PUSH_RETRY must be nonnegative")
	}
	for index, value := range cfg.BuildahOpts {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("PLUGIN_BUILDAH_OPT value %d must not be empty", index)
		}
	}

	// Kimia v1.0.26 parses these fields but implements them only in its
	// BuildKit path. Reject them here instead of launching a successful-looking
	// Buildah build that silently ignores the request.
	unsupported := []struct {
		set  bool
		name string
	}{
		{cfg.Attestation != "", "PLUGIN_ATTESTATION"},
		{len(cfg.Attest) > 0, "PLUGIN_ATTEST"},
		{len(cfg.BuildKitOpts) > 0, "PLUGIN_BUILDKIT_OPT"},
		{cfg.Sign, "PLUGIN_SIGN"},
		{cfg.CosignKey != "", "PLUGIN_COSIGN_KEY"},
		{cfg.CosignPasswordEnv != "", "PLUGIN_COSIGN_PASSWORD_ENV"},
	}
	for _, input := range unsupported {
		if input.set {
			return fmt.Errorf("%s is not supported by Kimia v1.0.26's Buildah backend", input.name)
		}
	}

	return nil
}
