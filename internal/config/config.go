package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/harness-community/drone-kimia/internal/envutil"
	"github.com/harness-community/drone-kimia/internal/tags"
)

type Config struct {
	Provider string

	KimiaPath      string
	Dockerfile     string
	Context        string
	ContextSubPath string
	Registry       string
	Repo           string
	Tags           []string
	Destinations   []string
	ExpandRepo     bool

	BuildArgs []string
	Labels    []string
	Target    string
	Platform  string

	EnableCache  bool
	DisableCache bool
	CacheDir     string
	CacheRepo    string
	ImportCache  []string
	ExportCache  []string

	NoPush                  bool
	PushOnly                bool
	TarPath                 string
	SourceTarPath           string
	DigestFile              string
	ImageNameWithDigestFile string
	ArtifactFile            string
	DroneOutput             string

	Insecure           bool
	InsecurePull       bool
	InsecureRegistries []string
	ImageDownloadRetry int

	Verbosity         string
	LogTimestamp      bool
	Reproducible      bool
	Timestamp         string
	Attestation       string
	Attest            []string
	BuildKitOpts      []string
	Sign              bool
	CosignKey         string
	CosignPasswordEnv string

	GitBranch    string
	GitRevision  string
	GitTokenFile string
	GitTokenUser string
}

func Load(provider string) (Config, error) {
	var cfg Config
	cfg.Provider = provider
	cfg.KimiaPath = firstWithDefault("/usr/local/bin/kimia", "KIMIA_EXECUTABLE")
	cfg.Dockerfile = firstWithDefault("Dockerfile", "PLUGIN_DOCKERFILE")
	cfg.Context = firstWithDefault(".", "PLUGIN_CONTEXT")
	cfg.Context = strings.TrimPrefix(cfg.Context, "dir://")
	cfg.ContextSubPath = envutil.First("PLUGIN_CONTEXT_SUB_PATH")
	if provider == "docker" {
		cfg.Registry = envutil.First("PLUGIN_REGISTRY", "DOCKER_REGISTRY")
	} else {
		// Cloud wrappers use DOCKER_REGISTRY for the separate base-image
		// connector. Treating it as the destination would push to the wrong host.
		cfg.Registry = envutil.First("PLUGIN_REGISTRY")
	}
	cfg.Repo = envutil.First("PLUGIN_REPO")
	cfg.Destinations = envutil.Semicolon(envutil.First("PLUGIN_DESTINATIONS"))
	cfg.Target = envutil.First("PLUGIN_TARGET")
	cfg.Platform = envutil.First("PLUGIN_PLATFORM", "PLUGIN_CUSTOM_PLATFORM")
	cfg.CacheDir = envutil.First("PLUGIN_CACHE_DIR")
	cfg.CacheRepo = envutil.First("PLUGIN_CACHE_REPO")
	cfg.TarPath = envutil.First("PLUGIN_TAR_PATH", "PLUGIN_DESTINATION_TAR_PATH")
	cfg.SourceTarPath = envutil.First("PLUGIN_SOURCE_TAR_PATH")
	cfg.DigestFile = envutil.First("PLUGIN_DIGEST_FILE")
	cfg.ImageNameWithDigestFile = envutil.First("PLUGIN_IMAGE_NAME_WITH_DIGEST_FILE")
	cfg.ArtifactFile = envutil.First("PLUGIN_ARTIFACT_FILE")
	cfg.DroneOutput = envutil.First("DRONE_OUTPUT")
	cfg.Verbosity = firstWithDefault("info", "PLUGIN_VERBOSITY")
	cfg.Timestamp = envutil.First("PLUGIN_TIMESTAMP")
	cfg.Attestation = envutil.First("PLUGIN_ATTESTATION")
	cfg.CosignKey = envutil.First("PLUGIN_COSIGN_KEY")
	cfg.CosignPasswordEnv = envutil.First("PLUGIN_COSIGN_PASSWORD_ENV")
	cfg.GitBranch = envutil.First("PLUGIN_GIT_BRANCH")
	cfg.GitRevision = envutil.First("PLUGIN_GIT_REVISION")
	cfg.GitTokenFile = envutil.First("PLUGIN_GIT_TOKEN_FILE")
	cfg.GitTokenUser = envutil.First("PLUGIN_GIT_TOKEN_USER")

	var err error
	for target, keys := range map[*bool][]string{
		&cfg.ExpandRepo:   {"PLUGIN_EXPAND_REPO"},
		&cfg.EnableCache:  {"PLUGIN_ENABLE_CACHE"},
		&cfg.DisableCache: {"PLUGIN_NO_CACHE"},
		&cfg.NoPush:       {"PLUGIN_NO_PUSH", "PLUGIN_DRY_RUN"},
		&cfg.PushOnly:     {"PLUGIN_PUSH_ONLY"},
		&cfg.Insecure:     {"PLUGIN_INSECURE"},
		&cfg.InsecurePull: {"PLUGIN_INSECURE_PULL"},
		&cfg.LogTimestamp: {"PLUGIN_LOG_TIMESTAMP"},
		&cfg.Reproducible: {"PLUGIN_REPRODUCIBLE"},
		&cfg.Sign:         {"PLUGIN_SIGN"},
	} {
		*target, err = envutil.Bool(keys...)
		if err != nil {
			return Config{}, err
		}
	}
	cfg.ImageDownloadRetry, err = envutil.Int("PLUGIN_IMAGE_DOWNLOAD_RETRY")
	if err != nil {
		return Config{}, err
	}

	if len(cfg.Destinations) == 0 {
		cfg.Tags = loadTags()
		expand, err := envutil.Bool("PLUGIN_EXPAND_TAG")
		if err != nil {
			return Config{}, err
		}
		automatic, err := envutil.Bool("PLUGIN_AUTO_TAG", "PLUGIN_DEFAULT_TAGS")
		if err != nil {
			return Config{}, err
		}
		cfg.Tags, err = tags.Resolve(
			cfg.Tags,
			expand,
			automatic,
			envutil.First("PLUGIN_AUTO_TAG_SUFFIX", "PLUGIN_DEFAULT_SUFFIX"),
			envutil.First("DRONE_COMMIT_REF"),
			envutil.First("DRONE_REPO_BRANCH"),
		)
		if err != nil {
			return Config{}, err
		}
	}

	multiple, err := envutil.Bool("PLUGIN_MULTIPLE_BUILD_ARGS")
	if err != nil {
		return Config{}, err
	}
	if multiple {
		cfg.BuildArgs = envutil.Semicolon(envutil.First("PLUGIN_BUILD_ARGS_NEW"))
	} else {
		cfg.BuildArgs = envutil.CSV(envutil.First("PLUGIN_BUILD_ARGS"))
	}
	cfg.BuildArgs = appendArgsFromEnvironment(cfg.BuildArgs, envutil.CSV(envutil.First("PLUGIN_BUILD_ARGS_FROM_ENV")))
	cfg.BuildArgs = appendProxyArgs(cfg.BuildArgs)
	cfg.Labels = envutil.CSV(envutil.First("PLUGIN_CUSTOM_LABELS"))
	cfg.ImportCache = append(
		legacyCacheImports(envutil.First("PLUGIN_CACHE_FROM")),
		envutil.Semicolon(envutil.First("PLUGIN_IMPORT_CACHE"))...,
	)
	cfg.ExportCache = append(
		envutil.Semicolon(envutil.First("PLUGIN_CACHE_TO")),
		envutil.Semicolon(envutil.First("PLUGIN_EXPORT_CACHE"))...,
	)
	cfg.InsecureRegistries = envutil.CSV(envutil.First("PLUGIN_INSECURE_REGISTRY"))
	cfg.Attest = envutil.Semicolon(envutil.First("PLUGIN_ATTEST"))
	cfg.BuildKitOpts = envutil.Semicolon(envutil.First("PLUGIN_BUILDKIT_OPT"))

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (cfg Config) Validate() error {
	if cfg.Repo == "" && len(cfg.Destinations) == 0 {
		return fmt.Errorf("PLUGIN_REPO or PLUGIN_DESTINATIONS is required; Kimia requires a destination even for build-only and tar export")
	}
	if cfg.Repo != "" && len(cfg.Destinations) > 0 {
		return fmt.Errorf("PLUGIN_REPO and PLUGIN_DESTINATIONS are mutually exclusive")
	}
	if len(cfg.Destinations) == 0 && len(cfg.Tags) == 0 {
		return fmt.Errorf("at least one PLUGIN_TAG/PLUGIN_TAGS value is required")
	}
	if cfg.EnableCache && cfg.DisableCache {
		return fmt.Errorf("PLUGIN_ENABLE_CACHE conflicts with PLUGIN_NO_CACHE")
	}
	if cfg.PushOnly && cfg.NoPush {
		return fmt.Errorf("PLUGIN_PUSH_ONLY conflicts with PLUGIN_NO_PUSH")
	}
	if cfg.PushOnly && cfg.TarPath != "" {
		return fmt.Errorf("PLUGIN_PUSH_ONLY conflicts with PLUGIN_TAR_PATH")
	}
	if cfg.PushOnly && strings.TrimSpace(cfg.SourceTarPath) == "" {
		return fmt.Errorf("PLUGIN_SOURCE_TAR_PATH is required when PLUGIN_PUSH_ONLY=true")
	}
	if cfg.Attestation != "" && len(cfg.Attest) > 0 {
		return fmt.Errorf("PLUGIN_ATTESTATION conflicts with PLUGIN_ATTEST")
	}
	if cfg.Sign && (cfg.CosignKey == "" || (cfg.Attestation == "" && len(cfg.Attest) == 0)) {
		return fmt.Errorf("PLUGIN_SIGN requires PLUGIN_COSIGN_KEY and an attestation input")
	}
	if !cfg.Sign && (cfg.CosignKey != "" || cfg.CosignPasswordEnv != "") {
		return fmt.Errorf("PLUGIN_COSIGN_KEY and PLUGIN_COSIGN_PASSWORD_ENV require PLUGIN_SIGN=true")
	}
	if cfg.Sign && (cfg.NoPush || cfg.TarPath != "") {
		return fmt.Errorf("PLUGIN_SIGN requires a registry push; it cannot be combined with build-only or tar export")
	}
	if cfg.Sign && cfg.PushOnly {
		return fmt.Errorf("PLUGIN_SIGN is not supported with PLUGIN_PUSH_ONLY")
	}
	if cfg.Sign && strings.EqualFold(strings.TrimSpace(cfg.Attestation), "off") {
		return fmt.Errorf("PLUGIN_SIGN cannot be combined with PLUGIN_ATTESTATION=off")
	}
	if err := ValidateUnsupportedEnvironment(); err != nil {
		return err
	}
	return nil
}

func appendArgsFromEnvironment(args, names []string) []string {
	for _, name := range names {
		upper := strings.ToUpper(name)
		for _, key := range []string{name, upper, "HARNESS_" + upper} {
			if value := os.Getenv(key); value != "" {
				args = appendBuildArgIfMissing(args, name, value)
				break
			}
		}
	}
	return args
}

func appendProxyArgs(args []string) []string {
	for _, name := range []string{"http_proxy", "https_proxy", "no_proxy"} {
		upper := strings.ToUpper(name)
		value := envutil.First(name, upper, "HARNESS_"+upper)
		if value == "" || hasBuildArg(args, name) || hasBuildArg(args, upper) {
			continue
		}
		args = append(args, name+"="+value, upper+"="+value)
	}
	return args
}

func appendBuildArgIfMissing(args []string, name, value string) []string {
	if hasBuildArg(args, name) {
		return args
	}
	return append(args, name+"="+value)
}

func hasBuildArg(args []string, name string) bool {
	for _, arg := range args {
		key, _, _ := strings.Cut(arg, "=")
		if key == name {
			return true
		}
	}
	return false
}

func legacyCacheImports(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var values []string
	if strings.Contains(value, ";") || strings.HasPrefix(value, "type=") {
		values = envutil.Semicolon(value)
	} else {
		values = envutil.CSV(value)
	}
	for index, item := range values {
		if !strings.HasPrefix(item, "type=") {
			values[index] = "type=registry,ref=" + item
		}
	}
	return values
}

func loadTags() []string {
	if value := envutil.First("PLUGIN_TAG", "PLUGIN_TAGS"); value != "" {
		return envutil.CSV(value)
	}
	data, err := os.ReadFile(".tags")
	if err == nil {
		if result := envutil.LinesOrCSV(string(data)); len(result) > 0 {
			return result
		}
	}
	return []string{"latest"}
}

func firstWithDefault(fallback string, keys ...string) string {
	if value := envutil.First(keys...); value != "" {
		return value
	}
	return fallback
}
