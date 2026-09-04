# Kimia BuildKit on Harness: Runtime Findings

- Status: confirmed on 2026-09-01
- Scope: RapidFort Kimia `v1.0.26`, BuildKit backend, Harness CI with
  `KubernetesDirect` infrastructure

## Summary

The `drone-kimia` image override now reaches the expected compatibility
entrypoint and starts Kimia. The build then fails while Kimia starts its
rootless BuildKit daemon, before the Dockerfile is executed.

Kimia `v1.0.26` always starts BuildKit through RootlessKit. On the tested
Harness Kubernetes runner:

- the default container security context run denies RootlessKit's `/` mount
  setup;
- `privileged: true` changes the failure to a user-namespace denial consistent
  with the AppArmor restriction identified by RootlessKit; and
- adding `allowPrivilegeEscalation: true` does not change the privileged result.

Consequently, this BuildKit variant is not a zero-setting Kaniko image override
on this runner. Plugin arguments, authentication mapping, context adaptation,
and entrypoint aliases cannot resolve a container-runtime or host-policy denial.

## Confirmed startup path

All three runs reached the plugin and Kimia successfully:

```text
Plugin entrypoint specified
[INFO] Kimia - Kubernetes-Native OCI Image Builder v1.0.26
[INFO] Detected builder: BUILDKIT
[INFO] Build context prepared at: /home/kimia/.drone-kimia-.../context
[INFO] Authentication configured: 1 direct auths, 0 helpers, global store: false
[INFO] Starting BuildKit build...
```

This confirms that:

- Harness found and executed `/kaniko/kaniko-docker`;
- the `drone-kimia` wrapper parsed the Harness plugin inputs;
- registry authentication configuration was generated and workspace adaptation
  completed; and
- the failure is in the upstream BuildKit runtime startup, not the wrapper's
  entrypoint, input mapping, or authentication flow.

## Tested Harness stage settings

### 1. Default container security context

No `containerSecurityContext` was configured:

```yaml
infrastructure:
  type: KubernetesDirect
  spec:
    connectorRef: opk3saws
    namespace: default
    automountServiceAccountToken: true
    nodeSelector: {}
```

Result:

```text
[rootlesskit:child ] error: failed to share mount point: /: permission denied
[rootlesskit:parent] error: child exited: exit status 1
[FATAL] build failed: buildkitd failed to become ready after 30 seconds
drone-kimia: Kimia build failed: exit status 1
```

Finding: RootlessKit's `/` mount setup was denied by the runner. This log does
not identify which individual runtime policy caused the denial. The build never
reaches the Dockerfile.

### 2. Privileged container

The stage added:

```yaml
containerSecurityContext:
  privileged: true
```

Result:

```text
time="2026-09-01T14:12:40Z" level=warning \
  msg="[rootlesskit:parent] This error might have happened because \
  /proc/sys/kernel/apparmor_restrict_unprivileged_userns is set to 1" \
  error="fork/exec /proc/self/exe: permission denied"
[rootlesskit:parent] error: failed to start the child: \
  fork/exec /proc/self/exe: permission denied
[FATAL] build failed: buildkitd failed to become ready after 30 seconds
drone-kimia: Kimia build failed: exit status 1
```

Finding: privileged mode changes the failure, but Kimia still runs as UID/GID
`1000:1000` and launches rootless BuildKit. The error is consistent with the
Ubuntu AppArmor unprivileged-user-namespace restriction identified by
RootlessKit. Confirming it as the enforcing policy requires checking the node's
sysctl and AppArmor audit log.

### 3. Privileged container with privilege escalation allowed

The stage added:

```yaml
containerSecurityContext:
  privileged: true
  allowPrivilegeEscalation: true
```

Result:

```text
time="2026-09-01T14:15:58Z" level=warning \
  msg="[rootlesskit:parent] This error might have happened because \
  /proc/sys/kernel/apparmor_restrict_unprivileged_userns is set to 1" \
  error="fork/exec /proc/self/exe: permission denied"
[rootlesskit:parent] error: failed to start the child: \
  fork/exec /proc/self/exe: permission denied
[FATAL] build failed: buildkitd failed to become ready after 30 seconds
drone-kimia: Kimia build failed: exit status 1
```

Finding: the result is identical to `privileged: true`. Privileged containers
already permit privilege escalation; the additional field cannot override a
host-side user-namespace policy such as the one indicated in the log.

## Root cause

Kimia `v1.0.26`'s BuildKit implementation always starts:

```text
rootlesskit ... --copy-up=/home ... buildkitd ...
```

It does not provide a rootful mode, reuse an inherited `BUILDKIT_HOST`, or fall
back to another backend. Kimia creates its own socket, starts a private
RootlessKit/buildkitd process, probes that socket with `buildctl --addr`, and
then supplies its private socket as `BUILDKIT_HOST` to the build command.

Rootless BuildKit needs mount and user-namespace operations that depend on the
pod security profile and node policy. The official BuildKit rootless
Kubernetes example uses unconfined seccomp and AppArmor profiles. Ubuntu also
requires an AppArmor profile permitting `userns` for unprivileged namespace
creation when `apparmor_restrict_unprivileged_userns=1`.

An OCI image can supply RootlessKit, BuildKit, UID/GID configuration, and
filesystem permissions. It cannot:

- select the pod's AppArmor or seccomp profile;
- load a required AppArmor profile on the Kubernetes node;
- change the node's AppArmor user-namespace sysctl;
- grant permissions that the container runtime or host LSM denies; or
- make Harness apply security settings that are not part of the image.

Harness's typed `containerSecurityContext` currently exposes settings such as
`privileged`, `allowPrivilegeEscalation`, user/group IDs, and capabilities, but
does not expose `appArmorProfile` or `seccompProfile`. Those profiles require a
runner-level mechanism, annotations or an applicable `podSpecOverlay`, plus any
necessary node configuration. The three tested stage configurations therefore
cannot express the complete upstream rootless BuildKit security contract.

## Compatibility conclusion

For the tested Harness Kubernetes runner, RapidFort Kimia `v1.0.26` with its
BuildKit backend cannot be a seamless Kaniko replacement through an image
override alone.

A viable BuildKit deployment requires an explicit runner/platform contract,
such as:

1. a node-loaded AppArmor policy allowing the RootlessKit executable to create
   the required user namespace, together with the required mount operations;
2. appropriate pod AppArmor and seccomp settings;
3. `allowPrivilegeEscalation: true` and the `SETUID`/`SETGID` capabilities used
   for rootless UID/GID mapping; and
4. node support for unprivileged user namespaces.

Using `privileged: true` alone is not sufficient on the tested node.
Automatically making every Kimia step privileged would also be a material
security regression from Kaniko and did not resolve the observed user-namespace
denial.

## Separate issues observed during testing

These failures are not BuildKit runtime failures:

- `fork/exec /kaniko/kaniko-docker: no such file or directory` meant the
  published provider image did not contain the Docker compatibility executable.
  The later Kimia startup logs prove that this entrypoint problem was resolved
  for the image used in the runtime tests above.
- `stat /harness/cmd/release-verify: directory not found` only proves that the
  path was absent from the command's `/harness` working directory. Possible
  causes include a source revision without the command or code being checked
  out at a different location. It occurs before any Kimia image runtime
  validation.

## References

- [Kimia v1.0.26 BuildKit startup implementation](https://github.com/rapidfort/kimia/blob/v1.0.26/src/internal/build/builder.go#L598-L1200)
- [Kimia v1.0.26 security requirements](https://github.com/rapidfort/kimia/blob/v1.0.26/docs/security.md)
- [BuildKit v0.32.2 rootless Kubernetes example](https://github.com/moby/buildkit/blob/v0.32.2/examples/kubernetes/job.rootless.yaml)
- [Official BuildKit rootless mode documentation](https://github.com/moby/buildkit/blob/v0.32.2/docs/rootless.md)
- [Ubuntu AppArmor unprivileged-user-namespace restrictions](https://documentation.ubuntu.com/security/security-features/privilege-restriction/apparmor/)
- [Rootless Containers: Ubuntu AppArmor requirements](https://rootlesscontaine.rs/getting-started/common/apparmor/)
- [Harness Kubernetes container security-context model](https://github.com/harness/harness-core/blob/develop/879-pipeline-ci-commons/src/main/java/io/harness/beans/yaml/extended/infrastrucutre/k8/SecurityContext.java)
