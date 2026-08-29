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
output_dir=$(mktemp -d)

cleanup() {
	case "${output_dir}" in
		"${TMPDIR:-/tmp}"/* | /tmp/* | /var/folders/*) rm -rf -- "${output_dir}" ;;
		*) echo "refusing to remove unexpected temporary path: ${output_dir}" >&2 ;;
	esac
}
trap cleanup EXIT HUP INT TERM

chmod 0777 "${output_dir}"

"${container_cli}" run --rm \
	-v "${repo_dir}/testdata/smoke:/home/kimia/workspace:ro" \
	"${image}" \
	--context /home/kimia/workspace \
	--dockerfile Dockerfile \
	--repo example.invalid/drone-kimia-smoke \
	--tags no-push \
	--no-push

"${container_cli}" run --rm \
	-v "${repo_dir}/testdata/smoke:/home/kimia/workspace:ro" \
	-v "${output_dir}:/home/kimia/output" \
	-e PLUGIN_CONTEXT=/home/kimia/workspace \
	-e PLUGIN_DOCKERFILE=Dockerfile \
	-e PLUGIN_REPO=example.invalid/drone-kimia-smoke \
	-e PLUGIN_TAG=tar \
	-e PLUGIN_TAR_PATH=/home/kimia/output/app.tar \
	-e PLUGIN_ARTIFACT_FILE=/home/kimia/output/artifact.json \
	-e DRONE_OUTPUT=/home/kimia/output/drone.env \
	"${image}"

[ -s "${output_dir}/app.tar" ] || {
	echo "${image}: tar smoke did not create app.tar" >&2
	exit 1
}
tar -tf "${output_dir}/app.tar" | grep -q '^manifest.json$' || {
	echo "${image}: tar smoke output is not a Docker archive" >&2
	exit 1
}
[ -s "${output_dir}/drone.env" ] || {
	echo "${image}: tar smoke did not create DRONE_OUTPUT" >&2
	exit 1
}
grep -Eq '^digest="?sha256:[0-9a-f]+"?$' "${output_dir}/drone.env" || {
	echo "${image}: DRONE_OUTPUT does not contain an image digest" >&2
	exit 1
}
grep -Fq 'IMAGE_TAR_PATH="/home/kimia/output/app.tar"' "${output_dir}/drone.env" || {
	echo "${image}: DRONE_OUTPUT does not contain IMAGE_TAR_PATH" >&2
	exit 1
}
[ -s "${output_dir}/artifact.json" ] || {
	echo "${image}: tar smoke did not create artifact.json" >&2
	exit 1
}
grep -Fq '"kind": "docker/v1"' "${output_dir}/artifact.json" \
	&& grep -Fq '"image": "example.invalid/drone-kimia-smoke:tar"' "${output_dir}/artifact.json" \
	&& grep -Eq '"digest": "sha256:[0-9a-f]+"' "${output_dir}/artifact.json" || {
	echo "${image}: artifact.json does not contain the expected image result" >&2
	exit 1
}

echo "${image}: CLI no-push, environment tar, DRONE_OUTPUT, and artifact smoke verified"
