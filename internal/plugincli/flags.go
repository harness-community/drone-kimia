package plugincli

import (
	"strings"

	"github.com/urfave/cli"
)

// semicolonSlice is repeatable without splitting commas that belong to a cache
// specification or build-argument value. It also accepts the native
// semicolon-separated environment syntax used by this wrapper.
type semicolonSlice struct {
	values []string
}

func (value *semicolonSlice) Set(input string) error {
	for _, item := range strings.Split(input, ";") {
		if item = strings.TrimSpace(item); item != "" {
			value.values = append(value.values, item)
		}
	}
	return nil
}

func (value *semicolonSlice) String() string {
	// urfave/cli includes Generic.String() in help as the default value. Never
	// expose resolved build arguments, cache credentials, or daemon options.
	return ""
}

func (value *semicolonSlice) joined() string {
	return strings.Join(value.values, ";")
}

// CommonFlags is the supported Buildah/Kimia contract shared by all provider
// images. Provider registry and authentication inputs are intentionally
// declared in each cmd/kimia-*/main.go.
func CommonFlags() []cli.Flag {
	return []cli.Flag{
		cli.StringFlag{
			Name:   "env-file",
			Usage:  "load plugin inputs from an environment file",
			EnvVar: "PLUGIN_ENV_FILE",
		},
		cli.StringFlag{
			Name:   "dockerfile",
			Usage:  "build Dockerfile",
			Value:  "Dockerfile",
			EnvVar: "PLUGIN_DOCKERFILE",
		},
		cli.StringFlag{
			Name:   "context",
			Usage:  "build context",
			Value:  ".",
			EnvVar: "PLUGIN_CONTEXT",
		},
		cli.StringFlag{
			Name:   "context-sub-path",
			Usage:  "subdirectory within the build context",
			EnvVar: "PLUGIN_CONTEXT_SUB_PATH",
		},
		cli.StringFlag{
			Name:   "repo",
			Usage:  "image repository",
			EnvVar: "PLUGIN_REPO",
		},
		cli.GenericFlag{
			Name:   "destinations",
			Usage:  "semicolon-separated complete image destinations",
			EnvVar: "PLUGIN_DESTINATIONS",
			Value:  new(semicolonSlice),
		},
		cli.BoolFlag{
			Name:   "expand-repo",
			Usage:  "prepend the registry when the repository has no registry host",
			EnvVar: "PLUGIN_EXPAND_REPO",
		},
		cli.StringSliceFlag{
			Name:   "tags",
			Usage:  "build tags",
			Value:  &cli.StringSlice{"latest"},
			EnvVar: "PLUGIN_TAG,PLUGIN_TAGS",
		},
		cli.BoolFlag{
			Name:   "expand-tag",
			Usage:  "enable semver tag expansion",
			EnvVar: "PLUGIN_EXPAND_TAG",
		},
		cli.BoolFlag{
			Name:   "auto-tag, tags.auto",
			Usage:  "enable automatic build tags",
			EnvVar: "PLUGIN_AUTO_TAG,PLUGIN_DEFAULT_TAGS",
		},
		cli.StringFlag{
			Name:   "auto-tag-suffix, tags.suffix",
			Usage:  "suffix for automatically generated tags",
			EnvVar: "PLUGIN_AUTO_TAG_SUFFIX,PLUGIN_DEFAULT_SUFFIX",
		},
		cli.StringFlag{
			Name:   "drone-commit-ref",
			Usage:  "commit reference used for automatic tags",
			EnvVar: "DRONE_COMMIT_REF",
		},
		cli.StringFlag{
			Name:   "drone-repo-branch",
			Usage:  "default branch used for automatic tags",
			EnvVar: "DRONE_REPO_BRANCH",
		},
		cli.StringSliceFlag{
			Name:   "args",
			Usage:  "build arguments",
			EnvVar: "PLUGIN_BUILD_ARGS",
		},
		cli.GenericFlag{
			Name:   "args-new",
			Usage:  "semicolon-separated build arguments that may contain commas",
			EnvVar: "PLUGIN_BUILD_ARGS_NEW",
			Value:  new(semicolonSlice),
		},
		cli.BoolFlag{
			Name:   "plugin-multiple-build-agrs, multiple-build-args",
			Usage:  "use the semicolon-separated args-new input",
			EnvVar: "PLUGIN_MULTIPLE_BUILD_ARGS",
		},
		cli.StringSliceFlag{
			Name:   "args-from-env",
			Usage:  "environment variable names to forward as build arguments",
			EnvVar: "PLUGIN_BUILD_ARGS_FROM_ENV",
		},
		cli.StringFlag{
			Name:   "target",
			Usage:  "Dockerfile target stage",
			EnvVar: "PLUGIN_TARGET",
		},
		cli.StringSliceFlag{
			Name:   "custom-labels",
			Usage:  "additional key=value image labels",
			EnvVar: "PLUGIN_CUSTOM_LABELS",
		},
		cli.StringFlag{
			Name:   "platform",
			Usage:  "target platform",
			EnvVar: "PLUGIN_PLATFORM,PLUGIN_CUSTOM_PLATFORM",
		},
		cli.StringFlag{
			Name:   "storage-driver",
			Usage:  "Buildah storage driver: vfs or overlay (blank uses Kimia's VFS default)",
			EnvVar: "PLUGIN_STORAGE_DRIVER",
		},
		cli.StringFlag{
			Name:   "snapshot-mode",
			Usage:  "Kaniko compatibility input; redo is accepted as a Buildah no-op",
			EnvVar: "PLUGIN_SNAPSHOT_MODE",
		},
		cli.BoolFlag{
			Name:   "daemon-off, daemon.off",
			Usage:  "Docker compatibility input; true confirms Kimia's daemonless mode",
			EnvVar: "PLUGIN_DAEMON_OFF",
		},
		cli.BoolFlag{
			Name:   "enable-cache",
			Usage:  "enable Buildah layer caching",
			EnvVar: "PLUGIN_ENABLE_CACHE",
		},
		cli.BoolFlag{
			Name:   "no-cache",
			Usage:  "disable Buildah layer caching",
			EnvVar: "PLUGIN_NO_CACHE",
		},
		cli.StringFlag{
			Name:   "cache-repo",
			Usage:  "registry repository used for cache import and export",
			EnvVar: "PLUGIN_CACHE_REPO",
		},
		cli.GenericFlag{
			Name:   "cache-from",
			Usage:  "cache source repository or type=registry,ref=REPO specification",
			EnvVar: "PLUGIN_CACHE_FROM",
			Value:  new(semicolonSlice),
		},
		cli.GenericFlag{
			Name:   "cache-to",
			Usage:  "cache destination repository or type=registry,ref=REPO specification",
			EnvVar: "PLUGIN_CACHE_TO",
			Value:  new(semicolonSlice),
		},
		cli.GenericFlag{
			Name:   "import-cache",
			Usage:  "semicolon-separated registry cache imports translated for Buildah",
			EnvVar: "PLUGIN_IMPORT_CACHE",
			Value:  new(semicolonSlice),
		},
		cli.GenericFlag{
			Name:   "export-cache",
			Usage:  "semicolon-separated registry cache exports translated for Buildah",
			EnvVar: "PLUGIN_EXPORT_CACHE",
			Value:  new(semicolonSlice),
		},
		cli.BoolFlag{
			Name:   "no-push, dry-run",
			Usage:  "build without pushing an image",
			EnvVar: "PLUGIN_NO_PUSH,PLUGIN_DRY_RUN",
		},
		cli.BoolFlag{
			Name:   "push-only",
			Usage:  "push an existing image archive without rebuilding it",
			EnvVar: "PLUGIN_PUSH_ONLY",
		},
		cli.StringFlag{
			Name:   "source-tar-path",
			Usage:  "existing single-image Docker archive used by push-only",
			EnvVar: "PLUGIN_SOURCE_TAR_PATH",
		},
		cli.StringFlag{
			Name:   "tar-path",
			Usage:  "export the image as a tar archive",
			EnvVar: "PLUGIN_TAR_PATH,PLUGIN_DESTINATION_TAR_PATH",
		},
		cli.StringFlag{
			Name:   "digest-file",
			Usage:  "write the built image digest to a file",
			EnvVar: "PLUGIN_DIGEST_FILE",
		},
		cli.StringFlag{
			Name:   "image-name-with-digest-file",
			Usage:  "write the image name and digest to a file",
			EnvVar: "PLUGIN_IMAGE_NAME_WITH_DIGEST_FILE",
		},
		cli.StringFlag{
			Name:   "artifact-file",
			Usage:  "write the Harness docker/v1 artifact JSON",
			EnvVar: "PLUGIN_ARTIFACT_FILE",
		},
		cli.StringFlag{
			Name:   "metadata-file",
			Usage:  "Harness compatibility input; currently ignored",
			EnvVar: "PLUGIN_METADATA_FILE",
		},
		cli.StringFlag{
			Name:   "output-file",
			Usage:  "write Harness output variables",
			EnvVar: "DRONE_OUTPUT",
		},
		cli.BoolFlag{
			Name:   "insecure",
			Usage:  "allow insecure registry push behavior",
			EnvVar: "PLUGIN_INSECURE",
		},
		cli.BoolFlag{
			Name:   "insecure-pull",
			Usage:  "disable TLS verification while Buildah pulls base images",
			EnvVar: "PLUGIN_INSECURE_PULL",
		},
		cli.StringSliceFlag{
			Name:   "insecure-registry",
			Usage:  "registries that may use insecure transport",
			EnvVar: "PLUGIN_INSECURE_REGISTRY",
		},
		cli.IntFlag{
			Name:   "image-download-retry",
			Usage:  "number of Buildah base-image pull attempts",
			EnvVar: "PLUGIN_IMAGE_DOWNLOAD_RETRY",
		},
		cli.IntFlag{
			Name:   "push-retry",
			Usage:  "number of Buildah registry push attempts",
			EnvVar: "PLUGIN_PUSH_RETRY",
		},
		cli.StringFlag{
			Name:   "verbosity",
			Usage:  "Kimia log level",
			Value:  "info",
			EnvVar: "PLUGIN_VERBOSITY",
		},
		cli.BoolFlag{
			Name:   "log-timestamp",
			Usage:  "include timestamps in Kimia logs",
			EnvVar: "PLUGIN_LOG_TIMESTAMP",
		},
		cli.BoolFlag{
			Name:   "reproducible",
			Usage:  "enable reproducible image output",
			EnvVar: "PLUGIN_REPRODUCIBLE",
		},
		cli.StringFlag{
			Name:   "timestamp",
			Usage:  "set the source date epoch used by Kimia",
			EnvVar: "PLUGIN_TIMESTAMP",
		},
		cli.StringFlag{
			Name:   "git-branch",
			Usage:  "Git branch for a remote build context",
			EnvVar: "PLUGIN_GIT_BRANCH",
		},
		cli.StringFlag{
			Name:   "git-revision",
			Usage:  "Git revision for a remote build context",
			EnvVar: "PLUGIN_GIT_REVISION",
		},
		cli.StringFlag{
			Name:   "git-token-file",
			Usage:  "file containing a Git authentication token",
			EnvVar: "PLUGIN_GIT_TOKEN_FILE",
		},
		cli.StringFlag{
			Name:   "git-token-user",
			Usage:  "username used with the Git token",
			EnvVar: "PLUGIN_GIT_TOKEN_USER",
		},
		cli.StringFlag{
			Name:   "attestation",
			Usage:  "BuildKit compatibility input; unsupported by the Buildah image",
			EnvVar: "PLUGIN_ATTESTATION",
		},
		cli.GenericFlag{
			Name:   "attest",
			Usage:  "BuildKit compatibility input; unsupported by the Buildah image",
			EnvVar: "PLUGIN_ATTEST",
			Value:  new(semicolonSlice),
		},
		cli.GenericFlag{
			Name:   "buildkit-opt",
			Usage:  "BuildKit compatibility input; unsupported by the Buildah image",
			EnvVar: "PLUGIN_BUILDKIT_OPT",
			Value:  new(semicolonSlice),
		},
		cli.GenericFlag{
			Name:   "buildah-opt",
			Usage:  "semicolon-separated flags passed to Kimia's Buildah bud command",
			EnvVar: "PLUGIN_BUILDAH_OPT",
			Value:  new(semicolonSlice),
		},
		cli.BoolFlag{
			Name:   "sign",
			Usage:  "BuildKit compatibility input; unsupported by the Buildah image",
			EnvVar: "PLUGIN_SIGN",
		},
		cli.StringFlag{
			Name:   "cosign-key",
			Usage:  "BuildKit compatibility input; unsupported by the Buildah image",
			EnvVar: "PLUGIN_COSIGN_KEY",
		},
		cli.StringFlag{
			Name:   "cosign-password-env",
			Usage:  "BuildKit compatibility input; unsupported by the Buildah image",
			EnvVar: "PLUGIN_COSIGN_PASSWORD_ENV",
		},
		cli.BoolTFlag{
			Name:   "pull-image",
			Usage:  "pull base images (false is unsupported by Kimia)",
			EnvVar: "PLUGIN_PULL_IMAGE",
		},
	}
}
