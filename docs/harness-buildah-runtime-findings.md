# Kimia Buildah on Harness: Runtime Findings

- Status: confirmed on Harness on 2026-09-04
- Scope: RapidFort Kimia `v1.0.26`, Buildah 1.44.0, and Harness CI with
  `KubernetesDirect` infrastructure
- Decision: use the rootful Buildah image contract and require
  `containerSecurityContext.privileged: true` for every Harness step that
  builds an image

## Result

The corrected Kimia Buildah image successfully completed the Harness
build-only workflow, including a Dockerfile `RUN` instruction and Docker
archive export, when the stage was privileged.

Use this infrastructure setting:

```yaml
infrastructure:
  type: KubernetesDirect
  spec:
    containerSecurityContext:
      privileged: true
```

`allowPrivilegeEscalation: true` is not required as a separate setting.
Kubernetes treats privilege escalation as enabled for a privileged container.
Setting `allowPrivilegeEscalation: true` without granting the mount capability
does not make an otherwise non-privileged Buildah build work.

No other pipeline workaround is required:

- keep the existing `/var/run` shared path;
- keep the normal Harness context and relative tar paths;
- do not add `/home/kimia` paths;
- do not set `optimize: false`; and
- continue to replace the plugin image and use the existing plugin inputs.

This makes the image compatible with the Kaniko entrypoint and input contract,
but not with Kaniko's non-privileged security contract.

## Harness evidence

### Initial image: runtime-directory failure

Before the image packaging was corrected, all three security-context variants
failed during Buildah initialization:

| `privileged` | `allowPrivilegeEscalation` | Result |
| --- | --- | --- |
| `false` | `false` | `lstat /run/user: no such file or directory` |
| `false` | `true` | Same error |
| `true` | `true` | Same error |

Harness mounts the shared `/var/run` path over Alpine's `/var/run -> /run`
link. That hides the upstream image's `/run/user/1000` directory. Moving
`XDG_RUNTIME_DIR` to `/tmp/run` fixed this packaging problem. Running the image
as UID/GID `0:0` also avoids the rootless subordinate-ID setup that fails when
`no_new_privs` prevents the setuid mapping helpers from acquiring capabilities.

### Corrected image, non-privileged stage

With neither privileged mode nor privilege escalation enabled, the corrected
image passed the entrypoint, context, authentication, and Buildah startup
paths. It then failed at Buildah's context-overlay mount, before the first
Dockerfile step:

```text
[INFO] Detected builder: BUILDAH
[INFO] Build context prepared at: /home/kimia/.drone-kimia-.../context
[INFO] Authentication configured: 1 direct auths, 0 helpers, global store: false
[INFO]   BUILDAH_ISOLATION=chroot
[INFO] Executing: buildah bud ...

Error: mounting an overlay over build context directory: creating overlay
scaffolding for build context directory: mount overlay:/dev/shm/buildah-context-...,
data: lowerdir=/harness,upperdir=...,workdir=...,userxattr: permission denied

[FATAL] build failed: buildah build failed: exit status 125
```

This later error confirms that the `/run/user` fix worked. The remaining
failure is a Buildah mount-permission requirement, not a drone-kimia
entrypoint, authentication, context-path, or tar-path problem.

### Corrected image, privileged stage

With only `privileged: true` selected, the same build completed:

```text
STEP 1/3: FROM alpine:latest
STEP 2/3: RUN apk add --no-cache bash
STEP 3/3: CMD ["bash"]
COMMIT .../test:test
[INFO] Build completed successfully
[INFO] Exporting image to TAR: /home/kimia/.drone-kimia-.../output/image.tar
[INFO] Successfully exported using direct buildah push
[INFO] No push requested, skipping image push to registries
[INFO] Build completed successfully!
```

This validates the compatibility entrypoint, input conversion, registry
authentication setup, workspace proxy, Buildah build, and build-to-tar path on
the tested Harness runner.

## Root cause

Kimia executes `buildah bud` with the prepared context. drone-kimia exposes the
Harness workspace through a private symlink under `/home/kimia`; Buildah
resolves that symlink, which is why its diagnostic correctly reports
`lowerdir=/harness`.

Buildah 1.44.0 then unconditionally creates and mounts a short-lived overlay
over the build context. This happens before Dockerfile instructions are run.
The overlay is independent of the configured image-layer storage driver:

- VFS controls Buildah's image and layer storage, not this context overlay;
- chroot controls Dockerfile `RUN` isolation, not this context overlay;
- `TMPDIR=/dev/shm` relocates the overlay scaffolding but does not grant mount
  permission; and
- copying, moving, or symlinking the context cannot disable the mount.

The native overlay mount requires mount authority such as `CAP_SYS_ADMIN`.
UID 0 inside a container does not grant that capability by itself.
`allowPrivilegeEscalation` controls `no_new_privs`; it does not add
`CAP_SYS_ADMIN`. Privileged mode works because Kubernetes grants the container
all Linux capabilities and relaxes the runtime security restrictions that
otherwise block the mount.

Buildah 1.44.0 has no CLI, environment, or containers configuration option to
disable this context-overlay path. Kimia has no input that can remove the
requirement. The exact VFS-and-chroot behavior is tracked in the open upstream
[Buildah issue 6910](https://github.com/podman-container-tools/buildah/issues/6910).

## Direct Buildah behavior

This requirement comes from Buildah itself. Running the matching Buildah
version directly in the same Harness pod policy reaches the same mount
boundary; replacing drone-kimia with another thin Buildah wrapper does not
remove it. The Drone Buildah plugin likewise documents `SYS_ADMIN` as a
required container capability.

Privileged mode is not a universal requirement for every Buildah deployment.
A runner with a correctly configured user namespace or FUSE overlay path can
support other configurations, and a platform that exposes precise capability
controls may be able to grant a narrower runtime contract. On the tested
Harness runner, however, `privileged: true` is the only verified and exposed
configuration that works. It is therefore the supported setting for this
plugin iteration.

## Operation matrix

| Plugin operation | Starts Buildah build | Harness requirement |
| --- | --- | --- |
| Normal build and push | Yes | `privileged: true` |
| Build only with `no_push` | Yes | `privileged: true` |
| Build and Docker tar export | Yes | `privileged: true` |
| Push only from an existing tar | No | No Buildah mount requirement |

Push-only is implemented by drone-kimia with `go-containerregistry`; it does
not start Kimia or Buildah. If build-only and push-only steps share one Harness
stage security context, the stage will still be privileged because the build
step requires it.

## Selected image contract

The provider images retain these packaging choices:

| Property | Selected value | Reason |
| --- | --- | --- |
| Container user | `0:0` | Avoid rootless setuid/user-namespace mapping under `no_new_privs` |
| Runtime directory | `/tmp/run`, owner `0:0`, mode `0700` | Remain independent of the Harness `/var/run` mount |
| Build isolation | `chroot` | Kimia's packaged Buildah path |
| Image-layer storage | VFS through `CONTAINERS_STORAGE_CONF` | Avoid an OverlayFS/FUSE dependency for Buildah's persistent layer store |
| Temporary directory | `/dev/shm` | Keep temporary context-overlay scaffolding off the container root filesystem |
| Harness security context | `privileged: true` | Permit Buildah 1.44's mandatory context-overlay mount |

Root inside the image and a privileged Kubernetes container remain distinct
security states. The image metadata supplies UID 0 but cannot grant Kubernetes
privilege. Harness must set the stage security context explicitly.

## Compatibility classification

The tested Buildah image is:

- compatible with Harness's injected `/kaniko/kaniko-*` entrypoints;
- compatible with existing Kaniko/Docker plugin inputs covered by the adapter;
- compatible with connector-derived registry authentication passed as plugin
  environment variables;
- compatible with the existing `/harness` context, `/var/run` shared path, and
  relative tar paths; and
- not a non-privileged Kaniko drop-in.

Further validation is still required for every provider and target runner
class, including normal authenticated push, ECR, GAR, ACR, caching,
multi-stage Dockerfiles, `COPY --chown`, amd64, and arm64. The successful
Docker-registry build-only and tar-export run establishes the base Harness
runtime contract.

## References

- [Kimia v1.0.26 Buildah command construction](https://github.com/rapidfort/kimia/blob/v1.0.26/src/internal/build/builder.go#L159-L343)
- [Buildah 1.44.0 context-overlay setup](https://github.com/podman-container-tools/buildah/blob/v1.44.0/imagebuildah/build_linux.go#L20-L85)
- [Buildah 1.44.0 unconditional setup call](https://github.com/podman-container-tools/buildah/blob/v1.44.0/imagebuildah/build.go#L291-L317)
- [Buildah issue 6910: VFS/chroot context-overlay behavior](https://github.com/podman-container-tools/buildah/issues/6910)
- [Drone Buildah capability example](https://github.com/drone-plugins/drone-buildah/blob/master/README.md#L61-L71)
- [Kubernetes container security context](https://kubernetes.io/docs/tasks/configure-pod-container/security-context/)
- [Buildah issue 6947: `no_new_privs` single-mapping failure](https://github.com/podman-container-tools/buildah/issues/6947)
