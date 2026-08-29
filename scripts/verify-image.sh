#!/bin/sh

set -eu

if [ "$#" -ne 3 ]; then
	echo "usage: $0 IMAGE PROVIDER ARCH" >&2
	exit 2
fi

image=$1
provider=$2
arch=$3
container_cli=${KIMIA_CONTAINER_CLI:-docker}

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH= cd -- "${script_dir}/.." && pwd)
cd "${repo_dir}"

# shellcheck disable=SC1091
. ./versions.env

case "${provider}" in
	docker) title=drone-kimia ;;
	gar | ecr | acr) title="drone-kimia-${provider}" ;;
	*) echo "unsupported provider: ${provider}" >&2; exit 2 ;;
esac
case "${arch}" in
	amd64) base_digest=${KIMIA_AMD64_DIGEST} ;;
	arm64) base_digest=${KIMIA_ARM64_DIGEST} ;;
	*) echo "unsupported architecture: ${arch}" >&2; exit 2 ;;
esac

inspect() {
	"${container_cli}" image inspect --format "$1" "${image}"
}

assert_equal() {
	name=$1
	expected=$2
	actual=$3
	if [ "${actual}" != "${expected}" ]; then
		echo "${image}: ${name}=${actual}, expected ${expected}" >&2
		exit 1
	fi
}

assert_equal user 1000:1000 "$(inspect '{{.Config.User}}')"
assert_equal workdir /home/kimia "$(inspect '{{.Config.WorkingDir}}')"
assert_equal architecture "${arch}" "$(inspect '{{.Architecture}}')"
assert_equal entrypoint "[\"/usr/local/bin/kimia-${provider}\"]" "$(inspect '{{json .Config.Entrypoint}}')"

cmd=$(inspect '{{json .Config.Cmd}}')
case "${cmd}" in
	'' | null | '[]') ;;
	*) echo "${image}: Cmd=${cmd}, expected no inherited command" >&2; exit 1 ;;
esac

assert_equal title "${title}" "$(inspect '{{index .Config.Labels "org.opencontainers.image.title"}}')"
assert_equal source https://github.com/harness-community/drone-kimia "$(inspect '{{index .Config.Labels "org.opencontainers.image.source"}}')"
assert_equal version "${PLUGIN_VERSION:-dev}" "$(inspect '{{index .Config.Labels "org.opencontainers.image.version"}}')"
assert_equal revision "${PLUGIN_COMMIT:-unknown}" "$(inspect '{{index .Config.Labels "org.opencontainers.image.revision"}}')"
assert_equal base-digest "${base_digest}" "$(inspect '{{index .Config.Labels "org.opencontainers.image.base.digest"}}')"
if [ -n "${PLUGIN_CREATED:-}" ]; then
	assert_equal created "${PLUGIN_CREATED}" "$(inspect '{{index .Config.Labels "org.opencontainers.image.created"}}')"
fi

version_output=$("${container_cli}" run --rm "${image}" --version)
case "${version_output}" in
	*"provider=${provider}; kimia=${KIMIA_VERSION}"*) ;;
	*) echo "${image}: unexpected --version output: ${version_output}" >&2; exit 1 ;;
esac

buildkit_output=$("${container_cli}" run --rm --entrypoint buildkitd "${image}" --version)
case "${buildkit_output}" in
	*"v${KIMIA_BUILDKIT_VERSION}"*) ;;
	*) echo "${image}: unexpected BuildKit version: ${buildkit_output}" >&2; exit 1 ;;
esac

rootlesskit_output=$("${container_cli}" run --rm --entrypoint rootlesskit "${image}" --version)
case "${rootlesskit_output}" in
	*"${KIMIA_ROOTLESSKIT_VERSION}"*) ;;
	*) echo "${image}: unexpected RootlessKit version: ${rootlesskit_output}" >&2; exit 1 ;;
esac

echo "${image}: image contract verified"
