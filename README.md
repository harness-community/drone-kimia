# drone-kimia

`drone-kimia` is a thin Harness/Drone plugin adapter for the Buildah edition of
[RapidFort Kimia](https://github.com/rapidfort/kimia). It reads compatible
`drone-kaniko` and `drone-docker` plugin inputs, prepares standard Docker
registry authentication, converts supported inputs to Kimia arguments, and
executes the Kimia binary included in the image.

The initial scope is intentionally small:

- Buildah only, packaged from `ghcr.io/rapidfort/kimia-bud:1.0.26`
- Docker-compatible registries, GAR, ECR, and ACR
- Linux amd64 and arm64
- direct input-to-Kimia mappings, plus narrowly scoped Harness workspace and
  build-to-tar/push-only compatibility
- no GCR image, BuildKit backend, Docker daemon, or implicit engine fallback

The Buildah image replaces the earlier BuildKit package after RootlessKit could
not start under the tested Harness Kubernetes security policy. The observed
BuildKit behavior remains recorded in
[`docs/harness-buildkit-findings.md`](docs/harness-buildkit-findings.md).
The subsequent Buildah runtime diagnosis and selected image contract are in
[`docs/harness-buildah-runtime-findings.md`](docs/harness-buildah-runtime-findings.md).
Buildah removes RootlessKit and the private `buildkitd`. The provider images
run Buildah as UID/GID `0:0`, with chroot isolation and VFS storage, so the
image-override path does not depend on rootless setuid/user-namespace mapping.
Harness validation confirmed that every operation which invokes `buildah bud`
requires `containerSecurityContext.privileged: true` on the tested
`KubernetesDirect` runner. The image is therefore not a configuration-free or
non-privileged Kaniko replacement.

## Images

| Registry flow | Command | Harness compatibility entrypoint | Release image |
| --- | --- | --- | --- |
| Docker-compatible registry | `kimia-docker` | `/kaniko/kaniko-docker` | `plugins/kimia` |
| Google Artifact Registry | `kimia-gar` | `/kaniko/kaniko-gar` | `plugins/kimia-gar` |
| Amazon Elastic Container Registry | `kimia-ecr` | `/kaniko/kaniko-ecr` | `plugins/kimia-ecr` |
| Azure Container Registry | `kimia-acr` | `/kaniko/kaniko-acr` | `plugins/kimia-acr` |

Every image derives directly from the architecture-specific manifest of
`ghcr.io/rapidfort/kimia-bud:1.0.26`. The image contains Buildah 1.44.0 and
defaults to VFS storage. The derived image deliberately selects UID/GID `0:0`,
sets `XDG_RUNTIME_DIR=/tmp/run`, and creates that directory as root with mode
`0700`. It also sets `TMPDIR=/dev/shm`, so Buildah 1.44's temporary context
overlay is created on the standard OCI tmpfs instead of being nested on the
container root filesystem's overlay. This changes only the scaffolding
location; it does not remove the overlay mount or its privilege requirement.
The version and immutable index/platform digests are recorded in
[`versions.env`](versions.env); no image consumes an upstream `latest` tag.

The Buildah conversion does not add or alter release-pipeline steps, image
repositories, tags, manifest topology, or compatibility entrypoints. Its
packaging changes are confined to the pinned Dockerfile runtime, while the Go
adapter changes how supported plugin inputs are rendered. At runtime the call
chain is:

```text
Harness /kaniko entrypoint
  -> provider Go wrapper
  -> input, destination, and authentication normalization
  -> /usr/local/bin/kimia
  -> Buildah 1.44.0 (buildah bud, chroot isolation, VFS by default)
```

The provider wrapper never invokes Buildah directly. For normal builds, Kimia
detects the `buildah` executable in the packaged image and owns `buildah bud`,
archive export, and registry push. The deliberately separate push-only flow is
described below.

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
| `PLUGIN_ENABLE_CACHE`, `PLUGIN_NO_CACHE` | becomes Kimia `--cache=true` or `--cache=false`, which selects Buildah layer caching or `--no-cache` |
| `PLUGIN_CACHE_REPO` | enables caching and becomes both Buildah `--cache-from REPO` and `--cache-to REPO` through repeated `--buildah-opt` arguments |
| `PLUGIN_CACHE_FROM`, `PLUGIN_CACHE_TO` | repository values, or compatible `type=registry,ref=REPO[,mode=max]` specifications, are normalized to Buildah `--cache-from`/`--cache-to` repositories |
| `PLUGIN_NO_PUSH` or `PLUGIN_DRY_RUN` | `--no-push` |
| `PLUGIN_TAR_PATH` or `PLUGIN_DESTINATION_TAR_PATH` | exports a Docker archive; relative Harness workspace paths are staged for Kimia and copied back after success |
| `PLUGIN_PUSH_ONLY`, `PLUGIN_SOURCE_TAR_PATH` | loads a single-image Docker archive and pushes it to the resolved destinations without rebuilding |
| `PLUGIN_DIGEST_FILE`, `PLUGIN_IMAGE_NAME_WITH_DIGEST_FILE` | Kimia digest outputs for local/tar builds; verified remote manifest digests for normal pushes |
| `PLUGIN_INSECURE`, `PLUGIN_INSECURE_REGISTRY` | corresponding Kimia registry options |
| `PLUGIN_INSECURE_PULL` | `--insecure-pull`; disables Buildah TLS verification while pulling build inputs |
| `PLUGIN_STORAGE_DRIVER` | `--storage-driver=vfs|overlay`; blank uses the packaged VFS default |
| `PLUGIN_IMAGE_DOWNLOAD_RETRY`, `PLUGIN_PUSH_RETRY` | corresponding nonnegative Kimia retry counts |
| `PLUGIN_VERBOSITY`, `PLUGIN_LOG_TIMESTAMP` | corresponding Kimia logging options |
| `PLUGIN_REPRODUCIBLE` | `--reproducible` |
| `PLUGIN_GIT_BRANCH`, `PLUGIN_GIT_REVISION`, token inputs | corresponding Kimia Git context options |
| `PLUGIN_ARTIFACT_FILE`, `DRONE_OUTPUT` | wrapper output destinations after a successful build |
| `PLUGIN_SNAPSHOT_MODE=redo` | accepted as a no-op because Harness injects this Kaniko optimization hint; Buildah performs its own layer change detection |
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

Notable inputs still rejected include `PLUGIN_CACHE_DIR`, registry/client
certificate inputs, TLS-skip aliases, registry and Docker daemon mirrors, and
engine-specific Kaniko, Docker daemon, or Buildx controls. BuildKit-only
`PLUGIN_ATTESTATION`, `PLUGIN_ATTEST`, `PLUGIN_BUILDKIT_OPT`, `PLUGIN_SIGN`,
`PLUGIN_COSIGN_KEY`, and `PLUGIN_COSIGN_PASSWORD_ENV` are also rejected before
Kimia starts. Kimia v1.0.26's Buildah path does not implement those requests,
so the wrapper does not report a misleading success.

## Kimia-native inputs

These inputs expose Kimia/Buildah behavior without pretending it is a Kaniko or
Docker flag. Use a semicolon between repeated values when a value itself may
contain commas.

| Input | Kimia behavior |
| --- | --- |
| `PLUGIN_DESTINATIONS` | semicolon-separated complete image destinations; becomes repeated `--destination` |
| `PLUGIN_CONTEXT_SUB_PATH` | `--context-sub-path` |
| `PLUGIN_IMPORT_CACHE` | semicolon-separated registry cache repositories, translated to Buildah `--cache-from` |
| `PLUGIN_EXPORT_CACHE` | semicolon-separated registry cache repositories, translated to Buildah `--cache-to` |
| `PLUGIN_TIMESTAMP` | `--timestamp` |
| `PLUGIN_BUILDAH_OPT` | semicolon-separated values passed through as repeated Kimia `--buildah-opt` arguments |
| `PLUGIN_STORAGE_DRIVER` | selects `vfs` or `overlay`; blank retains VFS |
| `PLUGIN_INSECURE_PULL` | Kimia `--insecure-pull` |
| `PLUGIN_IMAGE_DOWNLOAD_RETRY` | Kimia `--image-download-retry` |
| `PLUGIN_PUSH_RETRY` | Kimia `--push-retry` |

`PLUGIN_BUILDAH_OPT` is an escape hatch for a real Buildah `bud` option. Kimia
validates it and rejects flags that it manages itself, including the
Dockerfile, destination, build arguments, labels, target, platform, retry,
cache, isolation, user-namespace, capability, and security options. It should
not be used to work around the runner's security policy.

Cache inputs accept either a plain repository or the compatibility form
`type=registry,ref=REPO[,mode=max]`. Only `type=registry` is supported. The
wrapper extracts `ref` and passes the repository to Buildah. `mode=max` is
accepted for cache-export compatibility and is not forwarded because Buildah
already exports its intermediate cache images. Export `mode=min`, local/inline
cache types, malformed or unknown attributes, and `PLUGIN_CACHE_DIR` fail
explicitly instead of silently changing semantics.

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
  --cache=false \
  --no-push
```

This validates and executes the build, but it does not load the result into a
Docker daemon. Kimia detects Buildah and runs `buildah bud` with its chroot
isolation default. On the tested Harness Kubernetes runner this operation
requires stage-level `containerSecurityContext.privileged: true`. Use tar
export when the built image must be retained locally.

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
  --cache=false \
  --no-push \
  --tar-path /home/kimia/<private-output>/image.tar
```

With Buildah, Kimia builds the image locally and exports it through the Docker
archive transport: `buildah push IMAGE docker-archive:PATH`. `tar_path` selects
Kimia's archive-export path and skips the normal registry push. Setting
`no_push: true` as well is recommended because it states the build-only intent
explicitly and matches the existing Kaniko pipeline pattern. Harness already
mounts `/harness` as the shared workspace for every step, so this
workflow does not require another shared path or a `/home/kimia` path in the
pipeline. The existing `/var/run` shared path can also remain unchanged; the
image uses `/tmp/run` for its private runtime directory, so that mount does not
hide Buildah state. As with the existing build plugins, the Harness workspace
must be writable by the step. Build-to-tar begins with `buildah bud` and has
the same `privileged: true` requirement as a normal build.

The wrapper does not rely on Docker archive `RepoTags`. In a later step, it
loads the single image and applies the repository and tags supplied by the
current plugin step:

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
Kimia, Buildah, or a Docker daemon and does not enter Buildah's build runtime.
It therefore has no inherent Buildah privilege requirement. If it shares a
stage security context with the build-to-tar step, the stage remains
privileged because the build step requires it. The source must be a regular,
single-image Docker archive; zero-image or multi-image archives fail before
any push.

For a normal build-and-push, Kimia owns both `buildah bud` and the registry
push. Kimia v1.0.26 reports Buildah's config-blob digest through its digest
flags, rather than the pushed manifest digest expected by the existing plugin
contract. When a digest-derived output is requested, the wrapper therefore
lets Kimia finish the push and then reads the manifest digest back from each
destination using the same generated Docker authentication config. It writes
only those verified values to the digest file, image-name file, Harness
artifact, and `DRONE_OUTPUT`.

## Registry authentication

There are no Harness connector API calls in this plugin. Harness exposes
connector material as plugin environment inputs; the provider entrypoint reads
those inputs and writes the standard Docker config consumed by Kimia and
Buildah.

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
an incompatible global credential store so Buildah cannot silently select the
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
subprocess environment.

`HARNESS_CA_PATH` may be injected by the platform and is ignored rather than
treated as a requested build feature. This thin adapter does not mutate the
upstream Kimia trust store; private-CA requirements must be satisfied by the
runtime image or an upstream-supported trust configuration.

## Cache, TLS, and mirrors

`PLUGIN_CACHE_REPO` enables Kimia caching and maps the same repository to
Buildah `--cache-from` and `--cache-to`. `PLUGIN_CACHE_FROM`,
`PLUGIN_CACHE_TO`, `PLUGIN_IMPORT_CACHE`, and `PLUGIN_EXPORT_CACHE` are reduced
to registry repository references, deduplicated in input order, and passed as
repeated `--buildah-opt=--cache-from REPO` or `--buildah-opt=--cache-to REPO`
arguments. `PLUGIN_NO_CACHE=true` conflicts with any configured cache source or
destination instead of silently ignoring it.

`PLUGIN_INSECURE`, `PLUGIN_INSECURE_PULL`, and
`PLUGIN_INSECURE_REGISTRY` are passed to Kimia. The existing registry
certificate, client certificate, TLS-skip aliases, registry mirror, Docker
daemon mirror, and cache-TLS inputs are rejected because the thin adapter has
no direct, verified Buildah mapping for them. It does not patch Buildah's
configuration or add a sidecar workaround.

The packaged default is VFS. The only explicit storage values are `vfs` and
`overlay`; the older `native` value is rejected by this Buildah package.
`PLUGIN_STORAGE_DRIVER=overlay` is exposed because Kimia supports it, but it
changes the runner requirements and is not part of the image-override
compatibility target. Overlay must be tested separately on the intended
Kubernetes nodes.

## Migrating by image override

Keep the existing Harness step inputs when all configured inputs appear in the
compatible-input table above. Change the plugin image and enable privileged
mode at the Harness stage for every operation that builds an image:

```yaml
infrastructure:
  type: KubernetesDirect
  spec:
    containerSecurityContext:
      privileged: true
```

No separate `allowPrivilegeEscalation` setting is required. Enabling privilege
escalation alone is not a substitute for privileged mode on the tested runner.
Then use the corresponding replacement image:

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
root-owned alias to the provider's Kimia wrapper. It does not invoke Kaniko.
The provider image deliberately has a final UID/GID `0:0` runtime
contract, matching the compatibility behavior described below.

At the wrapper level, built-in Harness build-and-push steps do not require an
`optimize`, context, tar-path, or shared-path workaround. Harness's injected
`PLUGIN_SNAPSHOT_MODE=redo` is accepted as a Buildah no-op, and its GAR
`PLUGIN_METADATA_FILE` input is ignored. The adapter transparently exposes the
existing `/harness` context to Kimia and returns relative tar outputs to that
same shared workspace. Other nonempty engine-specific inputs remain explicit
errors when they have no truthful Buildah equivalent.

That input and filesystem compatibility is separate from the runtime security
contract. Harness testing confirmed that Buildah 1.44's context-overlay mount
fails in a non-privileged stage and succeeds with `privileged: true`. The image
no longer requires rootless subordinate-ID mapping, but its alias cannot grant
Kubernetes capabilities or alter AppArmor, seccomp, namespace, or mount policy.

VM steps may inject `PLUGIN_DAEMON_OFF=true`; Kimia accepts it because its
Buildah flow is daemonless. Harness's separate DLC/Buildx execution
mode replaces the executable with `dockerd-entrypoint.sh` and a Buildx binary,
not merely the image. That fixed Docker-daemon entrypoint is outside the thin
Kimia compatibility path and must not be overridden with these images.

## Runtime contract

The provider image keeps Kimia's Buildah tooling but deliberately changes the
upstream rootless runtime contract for Kaniko-style Harness compatibility:

- runtime user and group `0:0`
- `HOME=/home/kimia`
- `WORKDIR=/home/kimia`
- `XDG_RUNTIME_DIR=/tmp/run`, owned by root with mode `0700`
- Buildah 1.44.0 selected automatically by Kimia
- VFS storage explicitly selected through
  `CONTAINERS_STORAGE_CONF=/home/kimia/.config/containers/storage.conf`
- `TMPDIR=/dev/shm` for Buildah's temporary context-overlay scaffolding
- chroot isolation selected by Kimia for `buildah bud`
- no RootlessKit, private `buildkitd`, or Docker daemon

UID 0 inside the container is not Kubernetes privileged mode. The image cannot
request `privileged: true`, add host access, or bypass the pod's capability,
seccomp, AppArmor, namespace, mount, or `no_new_privs` policy. Harness must
supply `containerSecurityContext.privileged: true` for build operations. No
separate `allowPrivilegeEscalation` override is required when the container is
privileged. The image default was changed because UID 1000 also depends on
setuid `newuidmap`/`newgidmap` behavior that is unavailable when Harness applies
`allowPrivilegeEscalation: false`.

VFS avoids OverlayFS for persistent image-layer storage and does not require
`/dev/fuse` for that store; chroot avoids the RootlessKit daemon startup path
that failed in the earlier Harness test. Buildah 1.44 separately creates a
short-lived overlay over every build context. Directing its scaffolding to
`/dev/shm` does not remove that mount or the required mount capability. This
upstream behavior is why the tested Harness runner requires privileged mode
even with VFS and chroot. `XDG_RUNTIME_DIR=/tmp/run` keeps Buildah startup
independent of Alpine's `/var/run -> /run` link when Harness mounts its shared
`/var/run` volume. The image uses rootful Buildah and therefore does not need
subordinate UID/GID mapping for its default path.

Actual Harness testing established the base contract: non-privileged build-only
failed at the context-overlay mount, while the same Dockerfile and tar export
succeeded with `privileged: true`. Continue validation under that security
setting for multi-stage and `COPY --chown` behavior, normal authenticated push,
push-only, registry caching, all provider flows, and both architectures. A
local smoke reproduction with `/var/run` mounted and `no-new-privileges` is
host-specific evidence and does not establish non-privileged compatibility on
the target Harness runner. See
[`docs/harness-buildah-runtime-findings.md`](docs/harness-buildah-runtime-findings.md)
for the observed failures, selected contract, and validation boundary.

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

That command also verifies every provider image contract and runs the Docker
variant through Buildah/VFS build-only, normal build-and-push, tar export, and
push-only smoke paths. This local smoke is a packaging regression test; the
supported Harness build contract still requires stage-level privileged mode.

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
home/work directory and VFS configuration, deliberately set the final runtime
user to `0:0`, and clear upstream's inherited `--help` command before setting
the provider wrapper as the entrypoint.
