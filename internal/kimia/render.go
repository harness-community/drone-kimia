// Package kimia renders the normalized plugin configuration as a Kimia CLI
// invocation. It intentionally emits only options that affect Kimia's
// BuildKit backend in v1.0.26.
package kimia

import (
	"fmt"
	"strings"

	"github.com/harness-community/drone-kimia/internal/config"
)

// Command is a shell-free Kimia invocation suitable for exec.Command.
type Command struct {
	Path string
	Args []string
}

// Render builds a deterministic Kimia v1.0.26 BuildKit command. Destinations
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
	if err := validateBuildKitConfig(cfg, destinations); err != nil {
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

	imports, exports := cacheSpecs(cfg)
	if cfg.DisableCache {
		args = append(args, "--cache=false")
	} else if cfg.EnableCache || len(imports) > 0 || len(exports) > 0 {
		args = append(args, "--cache=true")
	}
	for _, value := range imports {
		args = append(args, "--import-cache="+value)
	}
	for _, value := range exports {
		args = append(args, "--export-cache="+value)
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
	for _, value := range cfg.InsecureRegistries {
		args = append(args, "--insecure-registry="+value)
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

	if cfg.Attestation != "" {
		args = append(args, "--attestation="+cfg.Attestation)
	}
	for _, value := range cfg.Attest {
		args = append(args, "--attest="+value)
	}
	for _, value := range cfg.BuildKitOpts {
		args = append(args, "--buildkit-opt="+value)
	}
	if cfg.Sign {
		args = append(args, "--sign")
	}
	if cfg.CosignKey != "" {
		args = append(args, "--cosign-key="+cfg.CosignKey)
	}
	if cfg.CosignPasswordEnv != "" {
		args = append(args, "--cosign-password-env="+cfg.CosignPasswordEnv)
	}

	return args, nil
}

func cacheSpecs(cfg config.Config) ([]string, []string) {
	imports := make([]string, 0, len(cfg.ImportCache)+1)
	for _, value := range cfg.ImportCache {
		imports = append(imports, normalizeCacheSpec(value, false))
	}
	exports := make([]string, 0, len(cfg.ExportCache)+1)
	for _, value := range cfg.ExportCache {
		exports = append(exports, normalizeCacheSpec(value, true))
	}
	if cfg.CacheRepo != "" {
		imports = append(imports, "type=registry,ref="+cfg.CacheRepo)
		exports = append(exports, "type=registry,ref="+cfg.CacheRepo+",mode=max")
	}
	return imports, exports
}

func normalizeCacheSpec(value string, export bool) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "type=") {
		return value
	}
	spec := "type=registry,ref=" + value
	if export {
		spec += ",mode=max"
	}
	return spec
}

func validateBuildKitConfig(cfg config.Config, destinations []string) error {
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

	// Kimia v1.0.26 parses these fields, but its BuildKit implementation does
	// not consume them. Failing here avoids a successful-looking no-op.
	unsupported := []struct {
		set  bool
		name string
	}{
		{cfg.CacheDir != "", "PLUGIN_CACHE_DIR"},
		{cfg.InsecurePull, "PLUGIN_INSECURE_PULL"},
		{cfg.ImageDownloadRetry != 0, "PLUGIN_IMAGE_DOWNLOAD_RETRY"},
	}
	for _, input := range unsupported {
		if input.set {
			return fmt.Errorf("%s is not supported by Kimia v1.0.26's BuildKit backend", input.name)
		}
	}

	return nil
}
