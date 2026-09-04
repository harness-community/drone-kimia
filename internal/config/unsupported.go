package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

var unsupportedInputs = []string{
	"PLUGIN_CACHE_TTL",
	"PLUGIN_CACHE_DIR",
	"PLUGIN_CACHE_COPY_LAYERS",
	"PLUGIN_CACHE_RUN_LAYERS",
	"PLUGIN_COMPRESSED_CACHING",
	"PLUGIN_SKIP_UNUSED_STAGES",
	"PLUGIN_CLEANUP",
	"PLUGIN_FORCE",
	"PLUGIN_LOG_FORMAT",
	"PLUGIN_OCI_LAYOUT_PATH",
	"PLUGIN_REGISTRY_CLIENT_CERT",
	"PLUGIN_REGISTRY_CERTIFICATE",
	"PLUGIN_SKIP_DEFAULT_REGISTRY_FALLBACK",
	"PLUGIN_SINGLE_SNAPSHOT",
	"PLUGIN_SKIP_PUSH_PERMISSION_CHECK",
	"PLUGIN_SKIP_TLS_VERIFY",
	"PLUGIN_SKIP_TLS_VERIFY_PULL",
	"PLUGIN_SKIP_TLS_VERIFY_REGISTRY",
	"PLUGIN_USE_NEW_RUN",
	"PLUGIN_IGNORE_VAR_RUN",
	"PLUGIN_IGNORE_PATH",
	"PLUGIN_IGNORE_PATHS",
	"PLUGIN_IMAGE_FS_EXTRACT_RETRY",
	"PLUGIN_IMAGE_NAME_TAG_WITH_DIGEST_FILE",
	"PLUGIN_MIRROR",
	"DOCKER_PLUGIN_MIRROR",
	"PLUGIN_REGISTRY_MIRRORS",
	"PLUGIN_STORAGE_PATH",
	"PLUGIN_BIP",
	"PLUGIN_MTU",
	"PLUGIN_CUSTOM_DNS",
	"PLUGIN_CUSTOM_DNS_SEARCH",
	"PLUGIN_IPV6",
	"PLUGIN_EXPERIMENTAL",
	"PLUGIN_DEBUG",
	"DOCKER_LAUNCH_DEBUG",
	"PLUGIN_DAEMON_RETRY_COUNT",
	"PLUGIN_SQUASH",
	"PLUGIN_COMPRESS",
	"PLUGIN_QUIET",
	"PLUGIN_LABEL_SCHEMA",
	"PLUGIN_AUTO_LABEL",
	"PLUGIN_REPO_LINK",
	"PLUGIN_PURGE",
	"PLUGIN_ADD_HOST",
	"PLUGIN_SECRET",
	"PLUGIN_SECRETS_FROM_ENV",
	"PLUGIN_SECRETS_FROM_FILE",
	"PLUGIN_SSH_AGENT_KEY",
	"PLUGIN_SOURCE_IMAGE",
	"PLUGIN_COSIGN_PRIVATE_KEY",
	"PLUGIN_COSIGN_PASSWORD",
	"PLUGIN_COSIGN_PARAMS",
	"PLUGIN_CREATE_REPOSITORY",
	"ECR_CREATE_REPOSITORY",
	"PLUGIN_LIFECYCLE_POLICY",
	"PLUGIN_REPOSITORY_POLICY",
	"PLUGIN_SCAN_ON_PUSH",
	"PLUGIN_SKIP_PUSH_IF_TAG_EXISTS",
	"PLUGIN_BUILDER_NAME",
	"PLUGIN_BUILDER_CONFIG",
	"PLUGIN_BUILDER_DRIVER",
	"PLUGIN_BUILDER_DRIVER_OPTS",
	"PLUGIN_BUILDER_DRIVER_OPTS_NEW",
	"PLUGIN_BUILDER_REMOTE_CONN",
	"PLUGIN_BUILDX_LOAD",
	"PLUGIN_BUILDX_OPTIONS",
	"PLUGIN_BUILDX_OPTIONS_SEMICOLON",
	"PLUGIN_BAKE_FILE",
	"PLUGIN_BAKE_OPTIONS",
	"PLUGIN_USE_LOADED_BUILDKIT",
	"PLUGIN_BUILDKIT_ASSETS_DIR",
	"PLUGIN_BUILDKIT_VERSION",
	"PLUGIN_CACHE_METRICS_FILE",
	"PLUGIN_PATH_STYLE",
	"AWS_PLUGIN_PATH_STYLE",
	"PLUGIN_CACHE_TLS_INSECURE",
	"PLUGIN_BUILDX_OUTPUT_FORMAT",
	"PLUGIN_BUILDKIT_INHERIT_AUTH",
	"ARTIFACT_REGISTRY",
	"PLUGIN_HARNESS_SELF_HOSTED_S3_ACCESS_KEY",
	"PLUGIN_HARNESS_SELF_HOSTED_S3_SECRET_KEY",
	"PLUGIN_HARNESS_SELF_HOSTED_GCP_JSON_KEY",
	"PLUGIN_HARNESS_SELF_HOSTED_AZURE_ACCOUNT_KEY",
	"PLUGIN_HARNESS_SELF_HOSTED_AZURE_TENANT_ID",
	"PLUGIN_HARNESS_SELF_HOSTED_AZURE_CLIENT_ID",
	"PLUGIN_HARNESS_SELF_HOSTED_AZURE_CLIENT_SECRET",
	"PLUGIN_HARNESS_SELF_HOSTED_AZURE_OIDC_TOKEN",
}

func ValidateUnsupportedEnvironment() error {
	// Harness injects this for VM build-and-push steps. Kimia never starts or
	// contacts a Docker daemon, so true already describes its runtime. False
	// would request the daemon-starting behavior of the Docker plugins and has
	// no truthful Kimia equivalent.
	if value := strings.TrimSpace(os.Getenv("PLUGIN_DAEMON_OFF")); value != "" {
		disabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("PLUGIN_DAEMON_OFF must be a boolean: %w", err)
		}
		if !disabled {
			return fmt.Errorf("PLUGIN_DAEMON_OFF=false is not supported; Kimia is always daemonless and cannot start a Docker daemon")
		}
	}

	// Harness injects redo for its Kaniko snapshot optimization. Buildah owns
	// layer change detection internally, so the value is accepted as a
	// compatibility no-op; accepting another mode would silently change its
	// meaning.
	if value := strings.TrimSpace(os.Getenv("PLUGIN_SNAPSHOT_MODE")); value != "" && value != "redo" {
		return fmt.Errorf("PLUGIN_SNAPSHOT_MODE=%q is not supported; only redo is accepted as a Buildah compatibility no-op", value)
	}

	for _, key := range unsupportedInputs {
		value, ok := os.LookupEnv(key)
		if !ok || strings.TrimSpace(value) == "" || explicitlyFalse(value) {
			continue
		}
		return fmt.Errorf("%s is not supported because Kimia v1.0.26 has no equivalent", key)
	}
	if value, ok := os.LookupEnv("PLUGIN_PULL_IMAGE"); ok {
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return fmt.Errorf("PLUGIN_PULL_IMAGE must be a boolean: %w", err)
		}
		if !parsed {
			return fmt.Errorf("PLUGIN_PULL_IMAGE=false is not supported by Kimia v1.0.26")
		}
	}
	return nil
}

func explicitlyFalse(value string) bool {
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	return err == nil && !parsed
}
