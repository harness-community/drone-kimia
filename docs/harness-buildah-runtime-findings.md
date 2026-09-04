# Kimia Buildah on Harness: Runtime Findings

- Status: root cause confirmed on 2026-09-04
- Scope: RapidFort Kimia `v1.0.26`, Buildah 1.44.0, Harness CI with
  `KubernetesDirect` infrastructure
- Decision: package the compatibility image with UID/GID `0:0`,
  `XDG_RUNTIME_DIR=/tmp/run`, chroot isolation, and VFS storage

## Summary

All three Harness runs reached `/kaniko/kaniko-docker`, the `drone-kimia`
wrapper, and Kimia successfully. They then failed before the Dockerfile was
executed with the same error:

```text
time="..." level=error msg="lstat /run/user: no such file or directory"
[FATAL] build failed: buildah build failed: exit status 1
drone-kimia: Kimia build failed: exit status 1
```

Changing `privileged` or `allowPrivilegeEscalation` did not affect this error.
It is a filesystem/runtime-directory problem, not proof that any of the tested
security contexts is insufficient.

The stage's existing `/var/run` shared path must remain unchanged. Alpine maps
`/var/run` to `/run`; mounting the shared volume at `/var/run` therefore hides
the image's baked `/run/user/1000` directory. The upstream Kimia Buildah image
also exports `XDG_RUNTIME_DIR=/run/user/1000`, so Buildah tries to resolve the
now-hidden parent directory during startup.

Moving `XDG_RUNTIME_DIR` to `/tmp/run` avoids the shared mount. A second,
independent issue then appears when the image remains UID/GID `1000:1000` and
the runtime applies `no_new_privs` (the effect required by
`allowPrivilegeEscalation: false`): the setuid `newuidmap` and `newgidmap`
helpers cannot acquire the capabilities needed for rootless UID/GID mapping.
The rootless fallback subsequently fails while setting supplemental groups.

For Kaniko-style image-override compatibility, the selected image contract is
therefore root inside the container (`0:0`), not rootless Buildah. This does
not make the Kubernetes container privileged and does not add host access or
capabilities. It lets Buildah use the permissions already available to the
container without relying on setuid escalation from UID 1000.

## Tested Harness settings

The existing stage also had `sharedPaths: [/var/run]` in every case. That is
normal Harness configuration and is not a pipeline mistake.

| Case | `privileged` | `allowPrivilegeEscalation` | Result |
| --- | --- | --- | --- |
| 1 | `false` | `false` | `lstat /run/user: no such file or directory` |
| 2 | `false` | `true` | Same error |
| 3 | `true` | `true` | Same error |

Common successful startup before the failure:

```text
Plugin entrypoint specified
[INFO] Kimia - Kubernetes-Native OCI Image Builder v1.0.26
[INFO] Detected builder: BUILDAH
[INFO] Build context prepared at: /home/kimia/.drone-kimia-.../context
[INFO] Authentication configured: 1 direct auths, 0 helpers, global store: false
[INFO] Using builder: BUILDAH
[INFO] Starting buildah build...
[INFO]   BUILDAH_ISOLATION=chroot
[INFO] Executing: buildah bud ...
```

This confirms that the compatibility entrypoint, input conversion,
authentication setup, and workspace preparation completed. The failure begins
inside Buildah initialization.

## Why `/run/user` fails

The effective filesystem relationship is:

```text
/var/run -> /run
Harness shared volume mounted at /var/run
  -> mount resolves to /run
  -> image content under /run is hidden
  -> /run/user/1000 is no longer visible
```

The relevant process path for the original UID 1000 image is:

```text
Kimia executes buildah bud
  -> Buildah mainInit calls storage.DefaultStoreOptions
  -> containers/storage selects rootless storage options
  -> homedir.GetRuntimeDir reads XDG_RUNTIME_DIR
  -> filepath.EvalSymlinks("/run/user/1000")
  -> lstat /run/user fails
```

This lookup happens while Buildah initializes, before `bud` evaluates the
Dockerfile and before its chroot isolation setting can help. It also explains
why all three security-context variants produced the same result.

## Why the rootless image is not the drop-in contract

Overriding `XDG_RUNTIME_DIR=/tmp/run` fixes the first failure. An exact local
reproduction with UID/GID `1000:1000`, the `/var/run` mount, and
`no-new-privileges` then reached rootless ID mapping and failed with:

```text
newgidmap: Could not set caps
newuidmap: Could not set caps
Falling back to single mapping
error setting supplemental groups list: operation not permitted
```

This is expected when setuid helpers execute under `no_new_privs`: they cannot
gain the capabilities used to install subordinate UID/GID mappings. Buildah
1.44.0 then falls back to a single mapping, whose supplemental-group setup is
also denied. Enabling privilege escalation or modifying node policy would be a
pipeline/runner workaround, not a seamless Kaniko image replacement.

This single-mapping failure is tracked in Buildah issue 6947 and its fix was
released in Buildah 1.45.0. Kimia `v1.0.26` contains Buildah 1.44.0, so the
root-default contract is the compatible choice for the currently pinned Kimia
release; a future Kimia update can be reevaluated separately.

## Selected image contract

The provider images use the following runtime contract:

| Property | Selected value | Reason |
| --- | --- | --- |
| Container user | `0:0` | Avoid rootless setuid/user-namespace mapping under `no_new_privs` |
| Privileged mode | Not requested | Image metadata does not grant Kubernetes privileged mode |
| Privilege escalation | Not required by the validated simple build | Compatible with `allowPrivilegeEscalation: false` for the tested path |
| Runtime directory | `/tmp/run`, owner `0:0`, mode `0700` | Independent of Harness's `/var/run` mount and valid for one user |
| Build isolation | `chroot` | Kimia's packaged Buildah path |
| Storage driver | `vfs`, explicitly selected with `CONTAINERS_STORAGE_CONF` | Avoid OverlayFS/FUSE and system-config differences |
| Temporary directory | `/dev/shm` | Keep Buildah temporary scaffolding off the container root overlay |

Root inside a container and a privileged container are different security
states. UID 0 remains limited by the pod's capability set, seccomp/AppArmor
profiles, namespaces, read-only mounts, and volume permissions. This choice is
still a security-posture change from the original Kimia rootless image and
must be reviewed as such; it is selected because the compatibility target is
the existing root-running Kaniko execution model without requesting
`privileged: true`.

## Validation evidence and remaining scope

Local container reproduction covered the same critical conditions:

| Runtime | `/var/run` mounted | `no-new-privileges` | Result |
| --- | --- | --- | --- |
| UID 1000, upstream XDG path | Yes | Either | Fails at `/run/user` lookup |
| UID 1000, `XDG_RUNTIME_DIR=/tmp/run` | Yes | Yes | Fails at rootless UID/GID mapping |
| UID 0, `XDG_RUNTIME_DIR=/tmp/run` | Yes | Yes | Builds a Dockerfile containing `RUN apk add --no-cache bash` |

The final provider images and release checks should preserve that last case as
a smoke test. It verifies the observed filesystem and `no_new_privs`
interaction, but does not replace target Harness validation across node
images, pod policies, Dockerfile features, architectures, registries, cache,
tar export, and push-only flows.

No `.harness` or pipeline change is required for this fix. In particular:

- keep the existing `/var/run` shared path;
- do not add `privileged: true`;
- do not add an `allowPrivilegeEscalation` override;
- do not add `/home/kimia`, `/tmp/run`, or special context/tar paths to the
  pipeline; and
- test the new alpha image by replacing only the plugin image.

## References

- [Kimia v1.0.26 Buildah image definition](https://github.com/rapidfort/kimia/blob/v1.0.26/Dockerfile.buildah#L157-L175)
- [Buildah 1.44.0 startup initialization](https://github.com/containers/buildah/blob/v1.44.0/cmd/buildah/main.go#L84-L101)
- [containers/storage runtime-directory selection](https://github.com/containers/storage/blob/main/types/options.go#L281-L305)
- [containers/storage `XDG_RUNTIME_DIR` resolution](https://github.com/containers/storage/blob/main/pkg/homedir/homedir_unix.go#L136-L181)
- [Buildah 1.44 rootless UID/GID mapping requirements](https://github.com/containers/buildah/blob/v1.44.0/docs/tutorials/05-openshift-rootless-build.md#L127-L136)
- [Buildah issue 6947: `no_new_privs` single-mapping failure](https://github.com/containers/buildah/issues/6947)
- [Buildah 1.45.0 release](https://github.com/podman-container-tools/buildah/releases/tag/v1.45.0)
- [XDG runtime-directory requirements](https://specifications.freedesktop.org/basedir/0.8/#variables)
