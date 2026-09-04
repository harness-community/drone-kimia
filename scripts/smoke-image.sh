#!/bin/sh

set -eu

if [ "$#" -ne 1 ]; then
	echo "usage: $0 IMAGE" >&2
	exit 2
fi

image=$1
container_cli=${KIMIA_CONTAINER_CLI:-docker}
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH= cd -- "${script_dir}/.." && pwd)
# shellcheck disable=SC1091
. "${repo_dir}/versions.env"
registry_image=${KIMIA_SMOKE_REGISTRY_IMAGE:-${SMOKE_REGISTRY_IMAGE}}
output_dir=$(mktemp -d)
workspace_dir=${output_dir}/harness
shared_run_dir=${output_dir}/var-run
registry_container=drone-kimia-smoke-registry-$$
registry_network=drone-kimia-smoke-network-$$
registry_started=false
network_created=false

cleanup() {
	if [ "${registry_started}" = true ]; then
		"${container_cli}" rm -f "${registry_container}" >/dev/null 2>&1 || true
	fi
	if [ "${network_created}" = true ]; then
		"${container_cli}" network rm "${registry_network}" >/dev/null 2>&1 || true
	fi
	case "${output_dir}" in
		"${TMPDIR:-/tmp}"/* | /tmp/* | /var/folders/*) rm -rf -- "${output_dir}" ;;
		*) echo "refusing to remove unexpected temporary path: ${output_dir}" >&2 ;;
	esac
}
trap cleanup EXIT HUP INT TERM

run_harness_builder() {
	"${container_cli}" run --rm \
		--security-opt=no-new-privileges \
		-v "${shared_run_dir}:/var/run" \
		"$@"
}

mkdir -p "${workspace_dir}" "${shared_run_dir}"
cp -R "${repo_dir}/testdata/smoke/." "${workspace_dir}/"
chmod 0777 "${output_dir}" "${workspace_dir}"
chmod 0755 "${shared_run_dir}"
[ -z "$(find "${shared_run_dir}" -mindepth 1 -maxdepth 1 -print -quit)" ] || {
	echo "${image}: Harness /var/run mount source must start empty" >&2
	exit 1
}

run_harness_builder \
	--entrypoint /bin/sh \
	-e "KIMIA_EXPECTED_NETAVARK_LOCK_PATH=${KIMIA_NETAVARK_LOCK_PATH}" \
	-e "KIMIA_EXPECTED_RUNTIME_USER=${KIMIA_RUNTIME_USER}" \
	-e "KIMIA_EXPECTED_STORAGE_CONF=${KIMIA_STORAGE_CONF}" \
	-e "KIMIA_EXPECTED_STORAGE_DRIVER=${KIMIA_STORAGE_DRIVER}" \
	-e "KIMIA_EXPECTED_STORAGE_RUNROOT=${KIMIA_STORAGE_RUNROOT}" \
	-e "KIMIA_EXPECTED_XDG_RUNTIME_DIR=${KIMIA_XDG_RUNTIME_DIR}" \
	"${image}" \
	-c '
		set -eu
		test "$(id -u):$(id -g)" = "${KIMIA_EXPECTED_RUNTIME_USER}"
		test "$(stat -c "%u:%g" "${HOME}")" = "0:0"
		test -w "${HOME}"
		test "${XDG_RUNTIME_DIR}" = "${KIMIA_EXPECTED_XDG_RUNTIME_DIR}"
		test "$(stat -c "%u:%g:%a" "${XDG_RUNTIME_DIR}")" = "0:0:700"
		test "${NETAVARK_LOCK_PATH}" = "${KIMIA_EXPECTED_NETAVARK_LOCK_PATH}"
		test "$(stat -c "%u:%g:%a" "${NETAVARK_LOCK_PATH%/*}")" = "0:0:700"
		test "${CONTAINERS_STORAGE_CONF}" = "${KIMIA_EXPECTED_STORAGE_CONF}"
		test "$(buildah info --format "{{.store.GraphDriverName}}|{{.store.RunRoot}}")" = "${KIMIA_EXPECTED_STORAGE_DRIVER}|${KIMIA_EXPECTED_STORAGE_RUNROOT}"
		test -d /var/run
		test ! -e /var/run/user
		grep -Eq "^NoNewPrivs:[[:space:]]+1$" /proc/self/status
	'

run_harness_builder \
	--workdir /harness \
	-v "${workspace_dir}:/harness" \
	-e HARNESS_WORKSPACE=/harness \
	-e DRONE_WORKSPACE=/harness \
	"${image}" \
	--context . \
	--dockerfile Dockerfile \
	--repo example.invalid/drone-kimia-smoke \
	--tags no-push \
	--snapshot-mode redo \
	--no-push

run_harness_builder \
	--entrypoint /kaniko/kaniko-docker \
	--workdir /harness \
	-v "${workspace_dir}:/harness" \
	-e HARNESS_WORKSPACE=/harness \
	-e DRONE_WORKSPACE=/harness \
	-e PLUGIN_REPO=example.invalid/drone-kimia-smoke \
	-e PLUGIN_TAG=tar \
	-e PLUGIN_NO_PUSH=true \
	-e PLUGIN_DESTINATION_TAR_PATH=imageci.tar \
	-e PLUGIN_SNAPSHOT_MODE=redo \
	-e PLUGIN_DAEMON_OFF=true \
	-e PLUGIN_METADATA_FILE=/addon/tmp/plugin-metadata.json \
	-e PLUGIN_ARTIFACT_FILE=/harness/artifact.json \
	-e DRONE_OUTPUT=/harness/drone.env \
	"${image}"

[ -s "${workspace_dir}/imageci.tar" ] || {
	echo "${image}: tar smoke did not return imageci.tar to the Harness workspace" >&2
	exit 1
}
tar -tf "${workspace_dir}/imageci.tar" | grep -q '^manifest.json$' || {
	echo "${image}: tar smoke output is not a Docker archive" >&2
	exit 1
}
[ -s "${workspace_dir}/drone.env" ] || {
	echo "${image}: tar smoke did not create DRONE_OUTPUT" >&2
	exit 1
}
grep -Eq '^digest="?sha256:[0-9a-f]+"?$' "${workspace_dir}/drone.env" || {
	echo "${image}: DRONE_OUTPUT does not contain an image digest" >&2
	exit 1
}
grep -Fq 'IMAGE_TAR_PATH="imageci.tar"' "${workspace_dir}/drone.env" || {
	echo "${image}: DRONE_OUTPUT does not contain IMAGE_TAR_PATH" >&2
	exit 1
}
[ -s "${workspace_dir}/artifact.json" ] || {
	echo "${image}: tar smoke did not create artifact.json" >&2
	exit 1
}
grep -Fq '"kind": "docker/v1"' "${workspace_dir}/artifact.json" \
	&& grep -Fq '"image": "example.invalid/drone-kimia-smoke:tar"' "${workspace_dir}/artifact.json" \
	&& grep -Eq '"digest": "sha256:[0-9a-f]+"' "${workspace_dir}/artifact.json" || {
	echo "${image}: artifact.json does not contain the expected image result" >&2
	exit 1
}

"${container_cli}" network create "${registry_network}" >/dev/null
network_created=true
"${container_cli}" run -d \
	--name "${registry_container}" \
	--network "${registry_network}" \
	"${registry_image}" >/dev/null
registry_started=true

run_harness_builder \
	--entrypoint /kaniko/kaniko-docker \
	--workdir /harness \
	--network "${registry_network}" \
	-v "${workspace_dir}:/harness" \
	-e HARNESS_WORKSPACE=/harness \
	-e DRONE_WORKSPACE=/harness \
	-e "PLUGIN_REPO=${registry_container}:5000/drone-kimia-smoke" \
	-e PLUGIN_TAG=normal \
	-e PLUGIN_INSECURE=true \
	-e PLUGIN_DIGEST_FILE=/harness/normal-digest \
	-e PLUGIN_IMAGE_NAME_WITH_DIGEST_FILE=/harness/normal-image-name \
	-e PLUGIN_ARTIFACT_FILE=/harness/normal-artifact.json \
	-e DRONE_OUTPUT=/harness/normal-drone.env \
	"${image}"

"${container_cli}" exec "${registry_container}" \
	test -f /var/lib/registry/docker/registry/v2/repositories/drone-kimia-smoke/_manifests/tags/normal/current/link || {
	echo "${image}: normal Kimia build-and-push did not reach the local registry" >&2
	exit 1
}
normal_digest=$(sed -n '1p' "${workspace_dir}/normal-digest")
printf '%s\n' "${normal_digest}" | grep -Eq '^sha256:[0-9a-f]{64}$' || {
	echo "${image}: normal push did not return a manifest digest" >&2
	exit 1
}
[ "$(cat "${workspace_dir}/normal-image-name")" = "${registry_container}:5000/drone-kimia-smoke@${normal_digest}" ] \
	&& grep -Fq "digest=\"${normal_digest}\"" "${workspace_dir}/normal-drone.env" \
	&& grep -Fq "\"image\": \"${registry_container}:5000/drone-kimia-smoke:normal\"" "${workspace_dir}/normal-artifact.json" \
	&& grep -Fq "\"digest\": \"${normal_digest}\"" "${workspace_dir}/normal-artifact.json" || {
	echo "${image}: normal push results do not contain the verified registry manifest digest" >&2
	exit 1
}

push_attempt=1
while :; do
	if run_harness_builder \
		--entrypoint /kaniko/kaniko-docker \
		--workdir /harness \
		--network "${registry_network}" \
		-v "${workspace_dir}:/harness" \
		-e HARNESS_WORKSPACE=/harness \
		-e DRONE_WORKSPACE=/harness \
		-e "PLUGIN_REPO=${registry_container}:5000/drone-kimia-smoke" \
		-e PLUGIN_TAG=push-only \
		-e PLUGIN_PUSH_ONLY=true \
		-e PLUGIN_SOURCE_TAR_PATH=imageci.tar \
		-e PLUGIN_INSECURE=true \
		-e PLUGIN_SNAPSHOT_MODE=redo \
		-e PLUGIN_ARTIFACT_FILE=/harness/push-artifact.json \
		-e DRONE_OUTPUT=/harness/push-drone.env \
		"${image}"; then
		break
	fi
	[ "${push_attempt}" -lt 10 ] || {
		echo "${image}: push-only smoke did not reach the local registry" >&2
		exit 1
	}
	push_attempt=$((push_attempt + 1))
	sleep 1
done

"${container_cli}" exec "${registry_container}" \
	test -f /var/lib/registry/docker/registry/v2/repositories/drone-kimia-smoke/_manifests/tags/push-only/current/link || {
	echo "${image}: local registry does not contain the pushed tag" >&2
	exit 1
}
[ -s "${workspace_dir}/push-drone.env" ] \
	&& grep -Eq '^digest="?sha256:[0-9a-f]+"?$' "${workspace_dir}/push-drone.env" \
	&& [ -s "${workspace_dir}/push-artifact.json" ] \
	&& grep -Fq "\"image\": \"${registry_container}:5000/drone-kimia-smoke:push-only\"" "${workspace_dir}/push-artifact.json" \
	&& grep -Eq '"digest": "sha256:[0-9a-f]+"' "${workspace_dir}/push-artifact.json" || {
	echo "${image}: push-only results do not contain the expected digest and image" >&2
	exit 1
}

if [ -e "${shared_run_dir}/user" ]; then
	echo "${image}: Buildah unexpectedly recreated /var/run/user in the Harness shared mount" >&2
	exit 1
fi

echo "${image}: rootful Buildah/VFS build-only, tar, normal-push, and push-only paths passed with Harness /var/run and no-new-privileges"
