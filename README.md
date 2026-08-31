# drone-kimia

`drone-kimia` is a thin Harness/Drone plugin adapter for the BuildKit edition of
[RapidFort Kimia](https://github.com/rapidfort/kimia). It reads compatible
`drone-kaniko` and `drone-docker` plugin inputs, prepares standard Docker
registry authentication, converts supported inputs to Kimia arguments, and
executes the Kimia binary included in the image.

The initial scope is intentionally small:

- BuildKit only
- Docker-compatible registries, GAR, ECR, and ACR
- Linux amd64 and arm64
- direct input-to-Kimia mappings, plus narrowly scoped Harness workspace and
  build-to-tar/push-only compatibility
- no GCR image, Buildah backend, Docker daemon, or implicit engine fallback

BuildKit is the only backend because it is the closest fit for Kaniko's
daemonless CI use case while retaining modern Dockerfile, cache, multi-platform,
and OCI output behavior. The plugin does not expose Buildah selection or carry
a second backend-specific compatibility layer.

## Images

| Registry flow | Command | Harness compatibility entrypoint | Release image |
| --- | --- | --- | --- |
| Docker-compatible registry | `kimia-docker` | `/kaniko/kaniko-docker` | `plugins/kimia` |
| Google Artifact Registry | `kimia-gar` | `/kaniko/kaniko-gar` | `plugins/kimia-gar` |
| Amazon Elastic Container Registry | `kimia-ecr` | `/kaniko/kaniko-ecr` | `plugins/kimia-ecr` |
| Azure Container Registry | `kimia-acr` | `/kaniko/kaniko-acr` | `plugins/kimia-acr` |

Every image derives directly from the architecture-specific manifest of Kimia
v1.0.26. The version and digests are recorded in [`versions.env`](versions.env);
no image consumes an upstream `latest` tag.

## Plugin CLI contract

Each image exposes the same `urfave/cli` v1-style interface as the existing
Drone plugins. Harness normally supplies settings as `PLUGIN_*` environment
variables, while local callers can use the corresponding named flags. Both
forms enter the same configuration and authentication paths; the CLI flags are
not help-only aliases.

For example, these two invocations are equivalent:

```sh
PLUGIN_CONTEXT=/workspace \
PLUGIN_REPO=registry.example/team/app \
PLUGIN_TAG=verify \
PLUGIN_NO_PUSH=true \
kimia-docker

kimia-docker \
  --context /workspace \
  --repo registry.example/team/app \
  --tags verify \
  --no-push
```

Run `kimia-docker --help`, `kimia-gar --help`, `kimia-ecr --help`, or
`kimia-acr --help` to see every supported flag and its environment aliases.
Shared build flags are defined in
[`internal/plugincli/flags.go`](internal/plugincli/flags.go). Registry and auth
flags are intentionally owned by the matching provider entrypoint:

- [`cmd/kimia-docker/main.go`](cmd/kimia-docker/main.go)
- [`cmd/kimia-gar/main.go`](cmd/kimia-gar/main.go)
- [`cmd/kimia-ecr/main.go`](cmd/kimia-ecr/main.go)
- [`cmd/kimia-acr/main.go`](cmd/kimia-acr/main.go)

`PLUGIN_ENV_FILE` is loaded before CLI environment aliases are resolved, as in
the reference plugins. Direct named flags take precedence over values loaded
from that file.

## Compatible existing inputs

The following inputs are accepted because they have a direct Kimia equivalent
or can be resolved before Kimia is invoked.

| Existing plugin input | Treatment |
| --- | --- |
| `PLUGIN_DOCKERFILE` | `--dockerfile`; defaults to `Dockerfile` |
| `PLUGIN_CONTEXT` | `--context`; a leading `dir://` is removed |
| `PLUGIN_REPO`, `PLUGIN_TAG`/`PLUGIN_TAGS` | converted to one Kimia `--destination` per resolved tag |
| `PLUGIN_REGISTRY`, `PLUGIN_EXPAND_REPO` | optionally prefixes the repository before destinations are created |
| `PLUGIN_EXPAND_TAG`, `PLUGIN_AUTO_TAG`, `PLUGIN_DEFAULT_TAGS`, suffix inputs | resolved by the wrapper using the existing plugin conventions |
| `PLUGIN_BUILD_ARGS` | comma-separated values become repeated `--build-arg` values |
| `PLUGIN_BUILD_ARGS_NEW` with `PLUGIN_MULTIPLE_BUILD_ARGS=true` | semicolon-separated values become repeated `--build-arg` values |
| `PLUGIN_BUILD_ARGS_FROM_ENV` | named environment values become build arguments |
| proxy environment variables | added as lowercase and uppercase build arguments unless explicitly provided |
| `PLUGIN_CUSTOM_LABELS` | repeated `--label` values |
| `PLUGIN_TARGET` | `--target` |
| `PLUGIN_PLATFORM` or `PLUGIN_CUSTOM_PLATFORM` | `--custom-platform` |
| `PLUGIN_ENABLE_CACHE`, `PLUGIN_NO_CACHE`, `PLUGIN_CACHE_REPO` | enables/disables cache; a cache repository becomes registry import and `mode=max` export specifications |
| `PLUGIN_CACHE_FROM`, `PLUGIN_CACHE_TO` | raw image references become BuildKit registry cache specifications; values beginning with `type=` pass through |
| `PLUGIN_NO_PUSH` or `PLUGIN_DRY_RUN` | `--no-push` |
| `PLUGIN_TAR_PATH` or `PLUGIN_DESTINATION_TAR_PATH` | exports a Docker archive; relative Harness workspace paths are staged for Kimia and copied back after success |
| `PLUGIN_PUSH_ONLY`, `PLUGIN_SOURCE_TAR_PATH` | loads a single-image Docker archive and pushes it to the resolved destinations without rebuilding |
| `PLUGIN_DIGEST_FILE`, `PLUGIN_IMAGE_NAME_WITH_DIGEST_FILE` | corresponding Kimia digest outputs |
| `PLUGIN_INSECURE`, `PLUGIN_INSECURE_REGISTRY` | corresponding Kimia registry options |
| `PLUGIN_VERBOSITY`, `PLUGIN_LOG_TIMESTAMP` | corresponding Kimia logging options |
| `PLUGIN_REPRODUCIBLE` | `--reproducible` |
| `PLUGIN_GIT_BRANCH`, `PLUGIN_GIT_REVISION`, token inputs | corresponding Kimia Git context options |
| `PLUGIN_ARTIFACT_FILE`, `DRONE_OUTPUT` | wrapper output destinations after a successful build |
| `PLUGIN_SNAPSHOT_MODE=redo` | accepted as a no-op because Harness injects this Kaniko optimization hint; BuildKit performs its own snapshotting |
| `PLUGIN_DAEMON_OFF=true` | accepted as a no-op because Harness injects it for VM steps and Kimia never starts a Docker daemon; `false` is rejected |
| `PLUGIN_METADATA_FILE` | accepted and ignored because Harness injects it for GAR |
| `PLUGIN_ENV_FILE` | loaded before other plugin inputs are evaluated |

`PLUGIN_PULL_IMAGE=true` is accepted as the existing default behavior.
`PLUGIN_PULL_IMAGE=false` is rejected because Kimia v1.0.26 has no equivalent.

Engine-specific Kaniko, Docker daemon, and Buildx options are not passed
through. A configured unsupported input fails before authentication or the
build begins. Boolean inputs explicitly set to `false` do not fail when false
means that the unsupported feature was not requested; an inverse request such
as `PLUGIN_DAEMON_OFF=false` is rejected because it asks Kimia to start a
Docker daemon. The source of truth is
[`internal/config/unsupported.go`](internal/config/unsupported.go).

Notable inputs rejected for the BuildKit backend include `PLUGIN_CACHE_DIR`,
`PLUGIN_INSECURE_PULL`, `PLUGIN_PUSH_RETRY`, `PLUGIN_IMAGE_DOWNLOAD_RETRY`,
`PLUGIN_REGISTRY_CERTIFICATE`, and `PLUGIN_STORAGE_DRIVER`. Kimia v1.0.26 may
parse some of these names, but its BuildKit path does not consume them, so the
wrapper does not report a misleading success.

## Kimia-native inputs

These inputs expose BuildKit features without pretending they are Kaniko or
Docker flags. Values containing BuildKit comma-separated specifications use a
semicolon between repeated specifications.

| Input | Kimia behavior |
| --- | --- |
| `PLUGIN_DESTINATIONS` | semicolon-separated complete image destinations; becomes repeated `--destination` |
| `PLUGIN_CONTEXT_SUB_PATH` | `--context-sub-path` |
| `PLUGIN_IMPORT_CACHE` | semicolon-separated repeated `--import-cache` specifications |
| `PLUGIN_EXPORT_CACHE` | semicolon-separated repeated `--export-cache` specifications |
| `PLUGIN_TIMESTAMP` | `--timestamp` |
| `PLUGIN_ATTESTATION` | simple Kimia `--attestation` mode |
| `PLUGIN_ATTEST` | semicolon-separated repeated `--attest` specifications |
| `PLUGIN_BUILDKIT_OPT` | semicolon-separated repeated `--buildkit-opt` values |
| `PLUGIN_SIGN` | enables Kimia signing after its required attestation inputs are supplied |
| `PLUGIN_COSIGN_KEY` | `--cosign-key` |
| `PLUGIN_COSIGN_PASSWORD_ENV` | `--cosign-password-env` |

`PLUGIN_ATTESTATION` and `PLUGIN_ATTEST` are mutually exclusive. Signing
requires `PLUGIN_COSIGN_KEY`, one of those attestation inputs, and a registry
push. Signing is rejected with `no_push`, tar export, or
`PLUGIN_ATTESTATION=off`, because Kimia cannot produce a useful signature in
those modes.

`PLUGIN_DESTINATIONS` is mutually exclusive with `PLUGIN_REPO`. Every direct
destination must include a tag, and all direct destinations in one step must
use the same explicit registry host. This keeps connector-derived credentials
and Harness artifact metadata unambiguous.

## Build-only

Kimia requires an image destination even when it does not push. A Harness
plugin step therefore still supplies `repo` and `tags`:

```yaml
type: Plugin
spec:
  image: plugins/kimia
  settings:
    context: .
    dockerfile: Dockerfile
    repo: registry.example.com/team/app
    tags: verify
    no_push: true
```

The wrapper invokes the equivalent of:

```text
/usr/local/bin/kimia \
  --context /home/kimia/<private-workspace-proxy> \
  --dockerfile Dockerfile \
  --destination registry.example.com/team/app:verify \
  --no-push
```

This validates and executes the build, but it does not load the result into a
Docker daemon. Use tar export when the built image must be retained locally.

## Tar export and push-only

Kimia v1.0.26 internally requires its local context and tar destination to be
under `/home/kimia`. The wrapper absorbs that implementation detail. When a
Harness step starts in `/harness`, the normal context `.` is exposed to Kimia
through a private path under its home. A relative tar output such as
`imageci.tar` is written privately and atomically copied back to
`/harness/imageci.tar` before the step succeeds:

```yaml
type: Plugin
spec:
  image: plugins/kimia
  settings:
    context: .
    dockerfile: Dockerfile
    repo: registry.example.com/team/app
    tags: verify
    no_push: true
    tar_path: imageci.tar
```

Equivalent Kimia arguments:

```text
/usr/local/bin/kimia \
  --context /home/kimia/<private-workspace-proxy> \
  --dockerfile Dockerfile \
  --destination registry.example.com/team/app:verify \
  --tar-path /home/kimia/<private-output>/image.tar
```

For the BuildKit backend, `--tar-path` selects Docker archive output instead of
registry push; `no_push` is optional when a tar path is present. Harness already
mounts `/harness` as the shared workspace for every step, so this workflow does
not require another shared path or a `/home/kimia` path in the pipeline. The
workspace must remain writable by the plugin's non-root UID 1000.

The v1.0.26 archive contains one image but leaves Docker `RepoTags` empty. The
wrapper does not rely on that field. In a later step, it loads the single image
and applies the repository and tags supplied by the current plugin step:

```yaml
type: Plugin
spec:
  image: plugins/kimia
  settings:
    repo: registry.example.com/team/app
    tags: verify
    push_only: true
    source_tar_path: imageci.tar
```

Push-only is implemented with the same `go-containerregistry` archive and
registry flow used by `drone-kaniko`. It reuses the selected Docker, GAR, ECR,
or ACR authentication, pushes directly over the registry API, and writes the
normal digest, Harness artifact, and `DRONE_OUTPUT` results. It does not start
Kimia, BuildKit, Buildah, or a Docker daemon and does not require privileged
mode. The source must be a regular, single-image Docker archive; zero-image or
multi-image archives fail before any push.

## Registry authentication

There are no Harness connector API calls in this plugin. Harness exposes
connector material as plugin environment inputs; the provider entrypoint reads
those inputs and writes the standard Docker config consumed by Kimia and
BuildKit.

Authentication is merged in this order:

1. `PLUGIN_CONFIG` or `DOCKER_PLUGIN_CONFIG`, otherwise an existing
   `$DOCKER_CONFIG/config.json`.
2. Separate base-image credentials from the compatible
   `PLUGIN_DOCKER_*`, `PLUGIN_BASE_IMAGE_*`, or `DOCKER_BASE_IMAGE_*` inputs.
3. Destination credentials resolved by the selected provider entrypoint.

The destination credential wins when the same host appears more than once.
The source Docker config is never modified. A private generated directory and
config file use modes `0700` and `0600`, are passed to Kimia through
`DOCKER_CONFIG`, and are deleted after the step. Explicit credentials replace
an incompatible global credential store so BuildKit cannot silently select the
wrong helper.

The shared base-image credential aliases are:

| Purpose | Registry | Username | Password |
| --- | --- | --- | --- |
| Preferred | `PLUGIN_DOCKER_REGISTRY` | `PLUGIN_DOCKER_USERNAME` | `PLUGIN_DOCKER_PASSWORD` |
| Compatibility | `PLUGIN_BASE_IMAGE_REGISTRY` | `PLUGIN_BASE_IMAGE_USERNAME` | `PLUGIN_BASE_IMAGE_PASSWORD` |
| Docker compatibility | `DOCKER_BASE_IMAGE_REGISTRY` | `DOCKER_BASE_IMAGE_USERNAME` | `DOCKER_BASE_IMAGE_PASSWORD` |

A partial base-image credential is an error. Cloud images also accept the older
`DOCKER_REGISTRY`/`DOCKER_USERNAME`/`DOCKER_PASSWORD` aliases at lower
precedence. The ECR image additionally accepts `PLUGIN_USERNAME` and
`PLUGIN_PASSWORD` as its lowest-priority base-image username/password aliases.

### Docker-compatible registries

`plugins/kimia` accepts `PLUGIN_USERNAME`/`PLUGIN_PASSWORD` and the existing
`DOCKER_USERNAME`/`DOCKER_PASSWORD` aliases. `ACCESS_TOKEN` is written as an
OAuth access-token Docker credential. With no explicit registry, Docker Hub's
canonical authentication key is used.

The matching CLI names are `--docker.registry`, `--docker.username`,
`--docker.password`, `--access-token`, and `--docker.config`. Separate
base-image credentials use `--docker.baseimageregistry`,
`--docker.baseimageusername`, and `--docker.baseimagepassword`.

Push registry precedence is `PLUGIN_REGISTRY`, then `DOCKER_REGISTRY`.
Configuration precedence is `PLUGIN_CONFIG`, then `DOCKER_PLUGIN_CONFIG`, then
the existing `$DOCKER_CONFIG/config.json`. Unknown top-level Docker config
fields, unrelated registry auth, and credential-helper entries are preserved.

### GAR

`plugins/kimia-gar` resolves its registry from `PLUGIN_REGISTRY` or
`PLUGIN_LOCATION` (`<location>-docker.pkg.dev`). It accepts JSON credentials
from `PLUGIN_JSON_KEY`, `GCR_JSON_KEY`, `GOOGLE_CREDENTIALS`, or `TOKEN`;
plain JSON and base64-encoded JSON are accepted. `PLUGIN_WORKLOAD_IDENTITY=true`
exchanges that credential for an OAuth access token.

Harness GAR steps provide `PLUGIN_REGISTRY` as `<host>/<project>`. The project
namespace is retained when constructing image and cache destinations, while
only the registry host is used as the Docker authentication key. A fully
qualified repository on the same GAR host is not prefixed a second time.

The existing environment-supplied OIDC flow is also accepted when all of these
are present: `PLUGIN_OIDC_TOKEN_ID`, `PLUGIN_PROJECT_NUMBER`, `PLUGIN_POOL_ID`,
`PLUGIN_PROVIDER_ID`, and `PLUGIN_SERVICE_ACCOUNT_EMAIL`. Partial OIDC input is
an error. There is no GCR provider image.

When none of those explicit GAR credentials is supplied, the wrapper selects
the bundled `docker-credential-gcr` helper for the GAR host so ambient Google
Application Default Credentials or a pod workload identity can be used. An
existing auth/helper mechanism for that host is preserved instead.

GAR auth is visible through `--registry`, `--location`, `--json-key`,
`--workload-identity`, and the OIDC tuple `--oidc-token-id`,
`--project-number`, `--pool-id`, `--provider-id`, and
`--service-account-email`.

### ECR

`plugins/kimia-ecr` uses the AWS SDK default credential chain and the existing
region/access-key/secret-key aliases. AssumeRole, external-ID, and environment
OIDC inputs are used when configured. The entrypoint obtains an ECR
authorization token and stores the returned Docker credential for Kimia.

| ECR value | Accepted inputs, in precedence order |
| --- | --- |
| Region | `PLUGIN_REGION`, `ECR_REGION`, `AWS_REGION` (default `us-east-1`) |
| Access key | `PLUGIN_ACCESS_KEY`, `ECR_ACCESS_KEY`, `AWS_ACCESS_KEY_ID` |
| Secret key | `PLUGIN_SECRET_KEY`, `ECR_SECRET_KEY`, `AWS_SECRET_ACCESS_KEY` |
| Session token | `PLUGIN_SESSION_TOKEN`, `AWS_SESSION_TOKEN` |
| Role | `PLUGIN_ASSUME_ROLE` |
| External ID | `PLUGIN_EXTERNAL_ID` |
| Web identity token | `PLUGIN_OIDC_TOKEN_ID`, used only with `PLUGIN_ASSUME_ROLE` |

With no explicit key pair, AWS profiles, IRSA/web-identity files, and instance
metadata remain available through the AWS SDK default chain.

ECR auth is visible through `--registry`, `--region`, `--access-key`,
`--secret-key`, `--session-token`, `--assume-role`, `--external-id`, and
`--oidc-token-id`. Repository creation, lifecycle/repository policies,
scan-on-push, and skip-existing-tag remain unsupported because they are ECR
management operations rather than Kimia argument or authentication mappings.

### ACR

`plugins/kimia-acr` accepts direct service-principal Docker credentials. It can
otherwise use the existing client-secret, client-certificate, OIDC assertion,
or Azure default-credential inputs, then exchange the Microsoft Entra token for
an ACR refresh token stored in Docker config.

| ACR value | Accepted inputs, in precedence order |
| --- | --- |
| Direct Docker login | `SERVICE_PRINCIPAL_CLIENT_ID` and `SERVICE_PRINCIPAL_CLIENT_SECRET` |
| Client ID | `CLIENT_ID`, `AZURE_CLIENT_ID`, `AZURE_APP_ID`, `PLUGIN_CLIENT_ID` |
| Client secret | `CLIENT_SECRET`, `PLUGIN_CLIENT_SECRET` |
| Base64 certificate | `CLIENT_CERTIFICATE`, `PLUGIN_CLIENT_CERTIFICATE` |
| Tenant ID | `TENANT_ID`, `AZURE_TENANT_ID`, `PLUGIN_TENANT_ID` |
| OIDC authority | `AZURE_AUTHORITY_HOST`, `PLUGIN_AZURE_AUTHORITY_HOST` |
| OIDC assertion | `PLUGIN_OIDC_TOKEN_ID` |

OIDC takes precedence over secret/certificate authentication; secret precedes
certificate; Azure default credentials are the final fallback. The authority
input applies only to OIDC. Every non-direct flow requires a tenant ID for the
ACR token exchange. A concrete public-cloud ACR host such as
`team.azurecr.io` is required; sovereign-cloud endpoints are rejected in this
initial release rather than mixing incompatible identity scopes.

ACR auth is visible through `--registry`, the direct
`--service-principal-client-id`/`--service-principal-client-secret` pair, and
`--client-id`, `--client-secret`, `--client-cert`, `--tenant-id`,
`--oidc-token-id`, and `--azure-authority-host`.

Simple build-only and tar-export runs do not make a destination-provider call
when no destination auth is configured. Authentication is prepared when the
step supplies explicit provider material, selects a concrete cloud registry,
or imports/exports a registry cache. This keeps private `FROM` images and
private cache operations working without forcing a cloud call for a local
`FROM scratch` build. A cache on a different host needs credentials in
`PLUGIN_CONFIG`.

After authentication is converted to the private Docker config, raw connector
passwords, keys, certificates, and OIDC assertions are removed from the Kimia
subprocess environment. The environment variable explicitly named by
`PLUGIN_COSIGN_PASSWORD_ENV` is retained when signing is enabled.

`HARNESS_CA_PATH` may be injected by the platform and is ignored rather than
treated as a requested build feature. This thin adapter does not mutate the
upstream Kimia trust store; private-CA requirements must be satisfied by the
runtime image or an upstream-supported trust configuration.

## Cache, TLS, and mirrors

`PLUGIN_CACHE_REPO` maps to a BuildKit registry import plus a `mode=max`
registry export. `PLUGIN_CACHE_FROM`/`PLUGIN_CACHE_TO` retain their existing
compatibility syntax, while the native `PLUGIN_IMPORT_CACHE` and
`PLUGIN_EXPORT_CACHE` inputs accept full BuildKit cache specifications. Use a
semicolon between repeated specifications because each specification can
contain commas.

`PLUGIN_INSECURE` and `PLUGIN_INSECURE_REGISTRY` are passed directly to Kimia.
The existing registry certificate, client certificate, TLS-skip, registry
mirror, Docker daemon mirror, and cache-TLS inputs are rejected: Kimia
v1.0.26's BuildKit path has no effective equivalent for them. The adapter does
not patch BuildKit configuration or add a sidecar workaround.

## Migrating by image override

Keep the existing Harness step settings and change only the plugin image when
all configured inputs appear in the compatible-input table above:

| Existing image | Kimia replacement |
| --- | --- |
| `plugins/kaniko` or `plugins/docker` | `plugins/kimia` |
| `plugins/kaniko-gar` or `plugins/gar` | `plugins/kimia-gar` |
| `plugins/kaniko-ecr` or `plugins/ecr` | `plugins/kimia-ecr` |
| `plugins/kaniko-acr` or `plugins/acr` | `plugins/kimia-acr` |

There is intentionally no replacement for a GCR image. For a normal registry
push, the step remains familiar:

```yaml
type: Plugin
spec:
  image: plugins/kimia
  settings:
    context: .
    dockerfile: Dockerfile
    repo: registry.example.com/team/app
    tags: ${<+codebase.shortCommitSha>}
    username: <+secrets.getValue("registry_username")>
    password: <+secrets.getValue("registry_password")>
```

Harness continues to expand connector details into environment/plugin inputs;
the plugin performs no Harness API or connector calls. If a complete
`PLUGIN_DESTINATIONS` value supplies the host, `PLUGIN_REGISTRY` may be omitted
and the wrapper authenticates that inferred host. If both are present, their
hosts must match.

Harness supplies explicit Kaniko entrypoints for its built-in build-and-push
steps even when the backend image is overridden. Each Kimia provider image
therefore exposes the matching `/kaniko/kaniko-*` compatibility path as a
root-owned, read-only alias to the provider's Kimia wrapper. It does not invoke
Kaniko or change the image's final non-root runtime contract.

For built-in Harness build-and-push steps, no `optimize`, context, tar-path, or
shared-path workaround is required. Harness's injected
`PLUGIN_SNAPSHOT_MODE=redo` is accepted as a BuildKit no-op, and its GAR
`PLUGIN_METADATA_FILE` input is ignored. The adapter transparently exposes the
existing `/harness` context to Kimia and returns relative tar outputs to that
same shared workspace. Other nonempty engine-specific inputs remain explicit
errors when they have no truthful BuildKit equivalent.

VM steps may inject `PLUGIN_DAEMON_OFF=true`; Kimia accepts it because its
BuildKit flow is already daemonless. Harness's separate DLC/Buildx execution
mode replaces the executable with `dockerd-entrypoint.sh` and a Buildx binary,
not merely the image. That fixed Docker-daemon entrypoint is outside the thin
Kimia compatibility path and must not be overridden with these images.

## Runtime contract

The plugin preserves the upstream Kimia image contract:

- runtime user and group `1000:1000`
- `HOME=/home/kimia`
- `WORKDIR=/home/kimia`
- rootless BuildKit started by Kimia for the duration of the build
- no Docker daemon and no Buildah fallback

The image does not add `privileged` mode or alter the upstream capability,
seccomp, AppArmor, or user-namespace requirements. The runner must support the
rootless user namespaces required by Kimia. Mounted contexts, cache volumes,
and output volumes must be accessible to UID 1000.

Kimia v1.0.26 starts BuildKit with process sandboxing disabled inside its
unprivileged container. This avoids privileged mode, but processes launched by
a Dockerfile `RUN` instruction share the plugin container's process namespace
boundary. Treat builds as untrusted workloads only on appropriately isolated
CI runners and re-evaluate this upstream setting on every Kimia upgrade.

## Development build

Development and CI use Go 1.26. The reproducible tool image is pinned to the
validated Go 1.26.7 multi-architecture manifest in [`versions.env`](versions.env).

The build script produces all four commands for Linux amd64 and arm64:

```sh
sh scripts/build.sh
```

To build all four images for the current host architecture:

```sh
sh scripts/docker.sh
```

Set `KIMIA_IMAGE_ARCH=amd64|arm64` to select an architecture explicitly and
`KIMIA_CONTAINER_CLI` to use a Docker-compatible CLI other than `docker`.

Then build a provider image with its architecture-specific Dockerfile, for
example:

```sh
docker build \
  -f docker/docker/Dockerfile.linux.amd64 \
  -t plugins/kimia:linux-amd64 \
  .
```

The derived Dockerfiles preserve `/usr/local/bin/kimia`, retain upstream's
non-root user, and clear upstream's inherited `--help` command before setting
the provider wrapper as the entrypoint.
