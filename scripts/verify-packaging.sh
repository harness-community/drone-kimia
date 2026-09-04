#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH= cd -- "${script_dir}/.." && pwd)
cd "${repo_dir}"

# shellcheck disable=SC1091
. ./versions.env

fail() {
	echo "packaging verification failed: $*" >&2
	exit 1
}

require_value() {
	name=$1
	eval "value=\${${name}:-}"
	[ -n "${value}" ] || fail "${name} is empty in versions.env"
}

require_contains() {
	file=$1
	value=$2
	grep -Fq -- "${value}" "${file}" || fail "${file} does not contain: ${value}"
}

validate_digest() {
	name=$1
	eval "value=\${${name}}"
	hex=${value#sha256:}
	[ "${hex}" != "${value}" ] || fail "${name} must start with sha256:"
	[ "${#hex}" -eq 64 ] || fail "${name} must contain 64 hexadecimal characters"
	case "${hex}" in
		*[!0-9a-f]*) fail "${name} must be lowercase hexadecimal" ;;
	esac
}

for name in \
	KIMIA_VERSION KIMIA_SOURCE_COMMIT KIMIA_BASE_IMAGE \
	KIMIA_INDEX_DIGEST KIMIA_AMD64_DIGEST KIMIA_ARM64_DIGEST \
	KIMIA_BUILDAH_VERSION KIMIA_STORAGE_DRIVER KIMIA_STORAGE_CONF KIMIA_STORAGE_RUNROOT \
	KIMIA_RUNTIME_USER KIMIA_XDG_RUNTIME_DIR KIMIA_NETAVARK_LOCK_PATH KIMIA_TMPDIR \
	GO_IMAGE DOCKER_PLUGIN_IMAGE \
	MANIFEST_PLUGIN_IMAGE SMOKE_REGISTRY_IMAGE; do
	require_value "${name}"
done

case "${KIMIA_VERSION}" in
	[0-9]*.[0-9]*.[0-9]*) ;;
	*) fail "KIMIA_VERSION must be a semantic version without a v prefix" ;;
esac

[ "${KIMIA_BASE_IMAGE}" = "ghcr.io/rapidfort/kimia-bud" ] \
	|| fail "KIMIA_BASE_IMAGE must select the pinned RapidFort Buildah variant"
[ "${KIMIA_STORAGE_DRIVER}" = "vfs" ] \
	|| fail "KIMIA_STORAGE_DRIVER must remain vfs for the nested Buildah image"
[ "${KIMIA_STORAGE_CONF}" = "/home/kimia/.config/containers/storage.conf" ] \
	|| fail "KIMIA_STORAGE_CONF must select the pinned upstream VFS configuration"
[ "${KIMIA_STORAGE_RUNROOT}" = "/tmp/containers/run" ] \
	|| fail "KIMIA_STORAGE_RUNROOT must remain on writable temporary storage"
[ "${KIMIA_RUNTIME_USER}" = "0:0" ] \
	|| fail "KIMIA_RUNTIME_USER must remain root for the Kaniko-compatible Harness runtime"
[ "${KIMIA_XDG_RUNTIME_DIR}" = "/tmp/run" ] \
	|| fail "KIMIA_XDG_RUNTIME_DIR must remain outside the Harness /var/run shared mount"
[ "${KIMIA_NETAVARK_LOCK_PATH}" = "/tmp/lock/netavark.lock" ] \
	|| fail "KIMIA_NETAVARK_LOCK_PATH must remain in the private root-owned lock directory"
[ "${KIMIA_TMPDIR}" = "/dev/shm" ] \
	|| fail "KIMIA_TMPDIR must use the OCI tmpfs to avoid nested-overlay context failures"

[ "${#KIMIA_SOURCE_COMMIT}" -eq 40 ] || fail "KIMIA_SOURCE_COMMIT must contain 40 hexadecimal characters"
case "${KIMIA_SOURCE_COMMIT}" in
	*[!0-9a-f]*) fail "KIMIA_SOURCE_COMMIT must be lowercase hexadecimal" ;;
esac

for name in KIMIA_INDEX_DIGEST KIMIA_AMD64_DIGEST KIMIA_ARM64_DIGEST; do
	validate_digest "${name}"
done

for image in "${GO_IMAGE}" "${DOCKER_PLUGIN_IMAGE}" "${MANIFEST_PLUGIN_IMAGE}" "${SMOKE_REGISTRY_IMAGE}"; do
	case "${image}" in
		*@sha256:*) ;;
		*) fail "release tool image is not digest pinned: ${image}" ;;
	esac
done

for provider in docker gar ecr acr; do
	if [ "${provider}" = docker ]; then
		image=plugins/kimia
		title=drone-kimia
		auth_help_input=PLUGIN_USERNAME
	else
		image="plugins/kimia-${provider}"
		title="drone-kimia-${provider}"
		case "${provider}" in
			gar) auth_help_input=PLUGIN_JSON_KEY ;;
			ecr) auth_help_input=PLUGIN_ACCESS_KEY ;;
			acr) auth_help_input=SERVICE_PRINCIPAL_CLIENT_ID ;;
		esac
	fi

	manifest="docker/${provider}/manifest.tmpl"
	[ -f "${manifest}" ] || fail "missing ${manifest}"
	require_contains "${manifest}" "image: ${image}:"
	require_contains "${manifest}" "${image}:{{#if build.tag}}{{trimPrefix \"v\" build.tag}}-{{/if}}linux-amd64"
	require_contains "${manifest}" "${image}:{{#if build.tag}}{{trimPrefix \"v\" build.tag}}-{{/if}}linux-arm64"
	require_contains "${manifest}" "variant: v8"

	for arch in amd64 arm64; do
		eval "digest=\${KIMIA_$(printf '%s' "${arch}" | tr '[:lower:]' '[:upper:]')_DIGEST}"
		dockerfile="docker/${provider}/Dockerfile.linux.${arch}"
		[ -f "${dockerfile}" ] || fail "missing ${dockerfile}"
		[ "$(grep -c '^FROM ' "${dockerfile}")" -eq 1 ] || fail "${dockerfile} must have exactly one FROM"
		require_contains "${dockerfile}" "FROM ${KIMIA_BASE_IMAGE}:${KIMIA_VERSION}@${digest}"
		require_contains "${dockerfile}" "ENV KIMIA_VERSION=${KIMIA_VERSION}"
		require_contains "${dockerfile}" "CONTAINERS_STORAGE_CONF=${KIMIA_STORAGE_CONF}"
		require_contains "${dockerfile}" "NETAVARK_LOCK_PATH=${KIMIA_NETAVARK_LOCK_PATH}"
		require_contains "${dockerfile}" "TMPDIR=${KIMIA_TMPDIR}"
		require_contains "${dockerfile}" "XDG_RUNTIME_DIR=${KIMIA_XDG_RUNTIME_DIR}"
		require_contains "${dockerfile}" "org.opencontainers.image.title=\"${title}\""
		require_contains "${dockerfile}" 'org.opencontainers.image.source="https://github.com/harness-community/drone-kimia"'
		require_contains "${dockerfile}" 'org.opencontainers.image.version="${PLUGIN_VERSION}"'
		require_contains "${dockerfile}" 'org.opencontainers.image.revision="${PLUGIN_REVISION}"'
		require_contains "${dockerfile}" "org.opencontainers.image.base.name=\"${KIMIA_BASE_IMAGE}:${KIMIA_VERSION}\""
		require_contains "${dockerfile}" "org.opencontainers.image.base.digest=\"${digest}\""
		require_contains "${dockerfile}" "COPY release/linux/${arch}/kimia-${provider} /usr/local/bin/kimia-${provider}"
		require_contains "${dockerfile}" "USER 0:0"
		require_contains "${dockerfile}" 'RUN mkdir -p /kaniko "${XDG_RUNTIME_DIR}" "${NETAVARK_LOCK_PATH%/*}"'
		require_contains "${dockerfile}" '&& chown -R 0:0 /home/kimia "${XDG_RUNTIME_DIR}" "${NETAVARK_LOCK_PATH%/*}"'
		require_contains "${dockerfile}" '&& chmod 0700 "${XDG_RUNTIME_DIR}" "${NETAVARK_LOCK_PATH%/*}"'
		require_contains "${dockerfile}" "&& chmod 0755 /kaniko"
		require_contains "${dockerfile}" "&& ln -s /usr/local/bin/kimia-${provider} /kaniko/kaniko-${provider}"
		require_contains "${dockerfile}" "RUN test \"\$(id -u):\$(id -g)\" = \"${KIMIA_RUNTIME_USER}\""
		require_contains "${dockerfile}" 'test "$(stat -c '\''%u:%g'\'' "${HOME}")" = "0:0"'
		require_contains "${dockerfile}" 'test -w "${HOME}"'
		require_contains "${dockerfile}" "test \"\${TMPDIR}\" = \"${KIMIA_TMPDIR}\""
		require_contains "${dockerfile}" "test \"\${XDG_RUNTIME_DIR}\" = \"${KIMIA_XDG_RUNTIME_DIR}\""
		require_contains "${dockerfile}" 'test "$(stat -c '\''%u:%g:%a'\'' "${XDG_RUNTIME_DIR}")" = "0:0:700"'
		require_contains "${dockerfile}" "test \"\${NETAVARK_LOCK_PATH}\" = \"${KIMIA_NETAVARK_LOCK_PATH}\""
		require_contains "${dockerfile}" 'test "$(stat -c '\''%u:%g:%a'\'' "${NETAVARK_LOCK_PATH%/*}")" = "0:0:700"'
		require_contains "${dockerfile}" 'test -w "$(pwd)"'
		require_contains "${dockerfile}" "&& test \"\$(readlink /kaniko/kaniko-${provider})\" = \"/usr/local/bin/kimia-${provider}\""
		require_contains "${dockerfile}" '&& test "$(stat -c '\''%u:%g:%a'\'' /kaniko)" = "0:0:755"'
		require_contains "${dockerfile}" "&& /kaniko/kaniko-${provider} --version"
		require_contains "${dockerfile}" "--help | grep -q \"PLUGIN_EXPAND_TAG\""
		require_contains "${dockerfile}" "--help | grep -q \"PLUGIN_PUSH_ONLY\""
		require_contains "${dockerfile}" "--help | grep -q \"PLUGIN_DAEMON_OFF\""
		require_contains "${dockerfile}" "--help | grep -q \"${auth_help_input}\""
		require_contains "${dockerfile}" "buildah --version | grep -q \"${KIMIA_BUILDAH_VERSION}\""
		require_contains "${dockerfile}" "driver[[:space:]]*=[[:space:]]*\"${KIMIA_STORAGE_DRIVER}\""
		require_contains "${dockerfile}" '"${CONTAINERS_STORAGE_CONF}"'
		require_contains "${dockerfile}" "ENTRYPOINT [\"/usr/local/bin/kimia-${provider}\"]"
		require_contains "${dockerfile}" "CMD []"
		last_user=$(awk '/^USER[[:space:]]+/ { user = $0 } END { print user }' "${dockerfile}")
		[ "${last_user}" = "USER ${KIMIA_RUNTIME_USER}" ] || fail "${dockerfile} must set the Kaniko-compatible root runtime user"
	done
done

require_contains scripts/smoke-image.sh 'shared_run_dir=${output_dir}/var-run'
require_contains scripts/smoke-image.sh '--security-opt=no-new-privileges'
require_contains scripts/smoke-image.sh '-v "${shared_run_dir}:/var/run"'
require_contains scripts/smoke-image.sh 'test "$(id -u):$(id -g)" = "${KIMIA_EXPECTED_RUNTIME_USER}"'
require_contains scripts/smoke-image.sh 'test "${XDG_RUNTIME_DIR}" = "${KIMIA_EXPECTED_XDG_RUNTIME_DIR}"'
require_contains scripts/smoke-image.sh 'test "${CONTAINERS_STORAGE_CONF}" = "${KIMIA_EXPECTED_STORAGE_CONF}"'
require_contains scripts/smoke-image.sh 'buildah info --format "{{.store.GraphDriverName}}|{{.store.RunRoot}}"'
require_contains scripts/smoke-image.sh 'Harness /var/run mount source must start empty'
require_contains scripts/smoke-image.sh 'test ! -e /var/run/user'
require_contains scripts/smoke-image.sh 'grep -Eq "^NoNewPrivs:[[:space:]]+1$" /proc/self/status'
require_contains scripts/smoke-image.sh 'Buildah unexpectedly recreated /var/run/user in the Harness shared mount'
require_contains testdata/smoke/Dockerfile 'FROM docker.io/library/alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b'
require_contains testdata/smoke/Dockerfile 'RUN apk add --no-cache bash'

if [ -f .drone.yml ]; then
	require_contains .drone.yml "image: ${GO_IMAGE}"
	require_contains .drone.yml "image: ${DOCKER_PLUGIN_IMAGE}"
	require_contains .drone.yml "image: ${MANIFEST_PLUGIN_IMAGE}"
	created_build_args=$(grep -c 'PLUGIN_CREATED=${DRONE_BUILD_CREATED:-unknown}' .drone.yml)
	[ "${created_build_args}" -eq 8 ] || fail ".drone.yml must pass PLUGIN_CREATED to all eight image builds"
fi

for harness_file in \
	.harness/harness.yaml \
	.harness/eventPR.yaml \
	.harness/eventPush.yaml \
	.harness/eventTag.yaml; do
	[ -f "${harness_file}" ] || fail "missing ${harness_file}"
done

require_contains .harness/harness.yaml "identifier: dronekimiaharness"
require_contains .harness/harness.yaml "connectorRef: GitHub_Harness_Community_Org"
require_contains .harness/harness.yaml "repoName: drone-kimia"
require_contains .harness/harness.yaml "image: ${GO_IMAGE}"
require_contains .harness/harness.yaml "image: ${DOCKER_PLUGIN_IMAGE}"
require_contains .harness/harness.yaml "image: ${MANIFEST_PLUGIN_IMAGE}"
require_contains .harness/harness.yaml 'repo: plugins/kimia'
require_contains .harness/harness.yaml 'dockerfile: docker/docker/Dockerfile.linux.amd64'
require_contains .harness/harness.yaml 'dockerfile: docker/docker/Dockerfile.linux.arm64'
require_contains .harness/harness.yaml 'repo: plugins/kimia-<+matrix.provider>'
require_contains .harness/harness.yaml 'dockerfile: docker/<+matrix.provider>/Dockerfile.linux.amd64'
require_contains .harness/harness.yaml 'dockerfile: docker/<+matrix.provider>/Dockerfile.linux.arm64'
require_contains .harness/harness.yaml 'spec: docker/<+matrix.repo>/manifest.tmpl'
require_contains .harness/harness.yaml 'password: <+secrets.getValue("Plugins_Docker_Hub_Pat")>'
require_contains .harness/harness.yaml 'PLUGIN_VERSION: <+codebase.tag>'
require_contains .harness/harness.yaml 'PLUGIN_COMMIT: <+codebase.commitSha>'
require_contains .harness/harness.yaml 'PLUGIN_BUILD_DATE: <+pipeline.startTs>'
require_contains .harness/harness.yaml 'PLUGIN_VERSION=<+codebase.tag>'
require_contains .harness/harness.yaml 'PLUGIN_REVISION=<+codebase.commitSha>'
require_contains .harness/harness.yaml 'PLUGIN_CREATED=<+pipeline.startTs>'
require_contains .harness/harness.yaml 'ignore_missing: "false"'
require_contains .harness/harness.yaml 'identifier: VerifyArchitectureImages'
require_contains .harness/harness.yaml 'command: go run -mod=readonly ./cmd/release-verify'
require_contains .harness/harness.yaml 'KIMIA_RELEASE_TAG: <+codebase.tag>'
require_contains .harness/harness.yaml 'KIMIA_RELEASE_REVISION: <+codebase.commitSha>'
require_contains .harness/harness.yaml 'DOCKER_USERNAME: drone'
require_contains .harness/harness.yaml 'DOCKER_PASSWORD: <+secrets.getValue("Plugins_Docker_Hub_Pat")>'

branch_build_arg_count=$(grep -c 'PLUGIN_REVISION: <+codebase.commitSha>' .harness/harness.yaml)
[ "${branch_build_arg_count}" -eq 4 ] || fail '.harness/harness.yaml must stamp the revision into all four branch image builds'

verify_step_line=$(grep -n 'identifier: VerifyArchitectureImages' .harness/harness.yaml | cut -d: -f1)
manifest_step_line=$(grep -n '                  identifier: Manifest$' .harness/harness.yaml | cut -d: -f1)
[ -n "${verify_step_line}" ] && [ -n "${manifest_step_line}" ] && [ "${verify_step_line}" -lt "${manifest_step_line}" ] \
	|| fail '.harness/harness.yaml must verify registry images before publishing manifests'

require_contains internal/releaseverify/releaseverify.go 'architectures = []string{"amd64", "arm64"}'
require_contains internal/releaseverify/releaseverify.go 'verifyUniqueDigests(verified)'
require_contains internal/releaseverify/releaseverify.go 'config.Config.Labels["org.opencontainers.image.revision"]'
require_contains internal/releaseverify/releaseverify.go 'remote.WithAuth(authenticator)'
require_contains internal/releaseverify/releaseverify.go 'verifyCompatibilityEntrypoint(image, provider)'
require_contains internal/releaseverify/releaseverify.go 'expectedRuntimeUser     = "0:0"'
require_contains internal/releaseverify/releaseverify.go 'expectedXDGRuntimeDir   = "/tmp/run"'
require_contains internal/releaseverify/releaseverify.go 'verifyRuntimeDirectory(image)'
require_contains internal/releaseverify/releaseverify.go 'aliasPath := "kaniko/kaniko-" + provider.name'
require_contains internal/releaseverify/releaseverify.go 'binaryPath := "usr/local/bin/kimia-" + provider.name'
require_contains cmd/release-verify/main.go 'Password:         os.Getenv("DOCKER_PASSWORD")'

if grep -Fq -- 'DOCKER_PASSWORD}' .harness/harness.yaml; then
	fail '.harness/harness.yaml must not interpolate the registry password into the verification command'
fi

if grep -Fq -- '<+matrix.image>' .harness/harness.yaml; then
	fail '.harness/harness.yaml must not use the cross-product image/repo publish matrix'
fi
if grep -Fq -- 'exclude:' .harness/harness.yaml; then
	fail '.harness/harness.yaml must not pair release repositories through matrix exclusions'
fi
matrix_concurrency_count=$(grep -c 'maxConcurrency: 1' .harness/harness.yaml)
[ "${matrix_concurrency_count}" -eq 5 ] || fail '.harness/harness.yaml must serialize all five release matrices'

for unsupported_harness_variant in gcr kaniko1.9.1 rf-plugins harnesssecure; do
	if grep -Fiq -- "${unsupported_harness_variant}" .harness/harness.yaml; then
		fail ".harness/harness.yaml includes unsupported variant: ${unsupported_harness_variant}"
	fi
done

harness_matrix_value_count() {
	matrix_key=$1
	matrix_expected=$2
	awk -v key="${matrix_key}" -v expected="${matrix_expected}" '
		$0 ~ "^[[:space:]]*" key ":[[:space:]]*$" {
			in_values = 1
			next
		}
		in_values && $0 ~ "^[[:space:]]*-[[:space:]]" {
			value = $0
			sub(/^[[:space:]]*-[[:space:]]*/, "", value)
			sub(/^"/, "", value)
			sub(/"$/, "", value)
			if (value == expected) {
				count++
			}
			next
		}
		in_values {
			in_values = 0
		}
		END {
			print count + 0
		}
	' .harness/harness.yaml
}

harness_matrix_value_total() {
	matrix_key=$1
	awk -v key="${matrix_key}" '
		$0 ~ "^[[:space:]]*" key ":[[:space:]]*$" {
			in_values = 1
			next
		}
		in_values && $0 ~ "^[[:space:]]*-[[:space:]]" {
			count++
			next
		}
		in_values {
			in_values = 0
		}
		END {
			print count + 0
		}
	' .harness/harness.yaml
}

[ "$(harness_matrix_value_total provider)" -eq 12 ] || fail ".harness/harness.yaml must have three providers in each of four serialized publish matrices"
for provider in gar ecr acr; do
	[ "$(harness_matrix_value_count provider "${provider}")" -eq 4 ] \
		|| fail ".harness/harness.yaml must include provider '${provider}' exactly four times"
done

[ "$(harness_matrix_value_total repo)" -eq 4 ] || fail ".harness/harness.yaml must have four provider directories in the manifest matrix"
for provider_directory in docker gar ecr acr; do
	[ "$(harness_matrix_value_count repo "${provider_directory}")" -eq 1 ] \
		|| fail ".harness/harness.yaml must include manifest provider directory '${provider_directory}' exactly once"
done

for event_file in eventPR eventPush eventTag; do
	require_contains ".harness/${event_file}.yaml" "identifier: ${event_file}"
	require_contains ".harness/${event_file}.yaml" "identifier: dronekimiaharness"
done

require_contains internal/plugincli/cli.go "const kimiaVersion = \"${KIMIA_VERSION}\""
require_contains internal/plugincli/cli.go "RapidFort Kimia"

unexpected_versions=$(grep -RhoE 'Kimia v[0-9]+\.[0-9]+\.[0-9]+' README.md internal 2>/dev/null \
	| grep -Fvx "Kimia v${KIMIA_VERSION}" || true)
[ -z "${unexpected_versions}" ] || fail "found Kimia version text inconsistent with versions.env: ${unexpected_versions}"

echo "packaging lock and release metadata are consistent"
