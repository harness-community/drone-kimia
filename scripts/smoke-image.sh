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

mkdir -p "${workspace_dir}"
cp -R "${repo_dir}/testdata/smoke/." "${workspace_dir}/"
chmod 0777 "${output_dir}" "${workspace_dir}"

"${container_cli}" run --rm \
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

"${container_cli}" run --rm \
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

push_attempt=1
while :; do
	if "${container_cli}" run --rm \
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

echo "${image}: native no-push and unchanged Harness tar-to-push workflow verified"
