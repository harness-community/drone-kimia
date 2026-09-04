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

assert_equal user "${KIMIA_RUNTIME_USER}" "$(inspect '{{.Config.User}}')"
assert_equal workdir /home/kimia "$(inspect '{{.Config.WorkingDir}}')"
assert_equal architecture "${arch}" "$(inspect '{{.Architecture}}')"
assert_equal entrypoint "[\"/usr/local/bin/kimia-${provider}\"]" "$(inspect '{{json .Config.Entrypoint}}')"

tmpdir=$(inspect '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^TMPDIR=//p')
assert_equal tmpdir "${KIMIA_TMPDIR}" "${tmpdir}"
xdg_runtime_dir=$(inspect '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^XDG_RUNTIME_DIR=//p')
assert_equal xdg-runtime-dir "${KIMIA_XDG_RUNTIME_DIR}" "${xdg_runtime_dir}"
storage_conf=$(inspect '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^CONTAINERS_STORAGE_CONF=//p')
assert_equal containers-storage-conf "${KIMIA_STORAGE_CONF}" "${storage_conf}"
netavark_lock_path=$(inspect '{{range .Config.Env}}{{println .}}{{end}}' | sed -n 's/^NETAVARK_LOCK_PATH=//p')
assert_equal netavark-lock-path "${KIMIA_NETAVARK_LOCK_PATH}" "${netavark_lock_path}"

cmd=$(inspect '{{json .Config.Cmd}}')
case "${cmd}" in
	'' | null | '[]') ;;
	*) echo "${image}: Cmd=${cmd}, expected no inherited command" >&2; exit 1 ;;
esac

assert_equal title "${title}" "$(inspect '{{index .Config.Labels "org.opencontainers.image.title"}}')"
assert_equal source https://github.com/harness-community/drone-kimia "$(inspect '{{index .Config.Labels "org.opencontainers.image.source"}}')"
assert_equal version "${PLUGIN_VERSION:-dev}" "$(inspect '{{index .Config.Labels "org.opencontainers.image.version"}}')"
assert_equal revision "${PLUGIN_COMMIT:-unknown}" "$(inspect '{{index .Config.Labels "org.opencontainers.image.revision"}}')"
assert_equal base-name "${KIMIA_BASE_IMAGE}:${KIMIA_VERSION}" "$(inspect '{{index .Config.Labels "org.opencontainers.image.base.name"}}')"
assert_equal base-digest "${base_digest}" "$(inspect '{{index .Config.Labels "org.opencontainers.image.base.digest"}}')"
if [ -n "${PLUGIN_CREATED:-}" ]; then
	assert_equal created "${PLUGIN_CREATED}" "$(inspect '{{index .Config.Labels "org.opencontainers.image.created"}}')"
fi

version_output=$("${container_cli}" run --rm "${image}" --version)
case "${version_output}" in
	*"provider=${provider}; kimia=${KIMIA_VERSION}"*) ;;
	*) echo "${image}: unexpected --version output: ${version_output}" >&2; exit 1 ;;
esac

compat_version_output=$("${container_cli}" run --rm \
	--entrypoint "/kaniko/kaniko-${provider}" \
	"${image}" \
	--version)
case "${compat_version_output}" in
	*"provider=${provider}; kimia=${KIMIA_VERSION}"*) ;;
	*) echo "${image}: unexpected Harness compatibility entrypoint output: ${compat_version_output}" >&2; exit 1 ;;
esac

compat_target=$("${container_cli}" run --rm \
	--entrypoint /bin/sh \
	"${image}" \
	-c "readlink /kaniko/kaniko-${provider}")
assert_equal Harness-entrypoint-target "/usr/local/bin/kimia-${provider}" "${compat_target}"

kaniko_metadata=$("${container_cli}" run --rm \
	--entrypoint /bin/sh \
	"${image}" \
	-c "stat -c '%u:%g:%a' /kaniko")
assert_equal Harness-entrypoint-directory 0:0:755 "${kaniko_metadata}"

buildah_output=$("${container_cli}" run --rm --entrypoint buildah "${image}" --version)
case "${buildah_output}" in
	*"${KIMIA_BUILDAH_VERSION}"*) ;;
	*) echo "${image}: unexpected Buildah version: ${buildah_output}" >&2; exit 1 ;;
esac

effective_storage=$("${container_cli}" run --rm \
	--security-opt=no-new-privileges \
	--entrypoint buildah \
	"${image}" \
	info --format '{{.store.GraphDriverName}}|{{.store.RunRoot}}')
assert_equal effective-buildah-storage "${KIMIA_STORAGE_DRIVER}|${KIMIA_STORAGE_RUNROOT}" "${effective_storage}"

if ! "${container_cli}" run --rm \
	--entrypoint /bin/sh \
	-e "KIMIA_EXPECTED_NETAVARK_LOCK_PATH=${KIMIA_NETAVARK_LOCK_PATH}" \
	-e "KIMIA_EXPECTED_RUNTIME_USER=${KIMIA_RUNTIME_USER}" \
	-e "KIMIA_EXPECTED_STORAGE_CONF=${KIMIA_STORAGE_CONF}" \
	-e "KIMIA_EXPECTED_STORAGE_DRIVER=${KIMIA_STORAGE_DRIVER}" \
	-e "KIMIA_EXPECTED_TMPDIR=${KIMIA_TMPDIR}" \
	-e "KIMIA_EXPECTED_XDG_RUNTIME_DIR=${KIMIA_XDG_RUNTIME_DIR}" \
	"${image}" \
	-c '
		set -eu
		test "$(id -u):$(id -g)" = "${KIMIA_EXPECTED_RUNTIME_USER}"
		test "$(stat -c "%u:%g" "${HOME}")" = "0:0"
		test -w "${HOME}"
		test -w "$(pwd)"
		test "${TMPDIR}" = "${KIMIA_EXPECTED_TMPDIR}"
		test -d "${TMPDIR}"
		test -w "${TMPDIR}"
		test "${XDG_RUNTIME_DIR}" = "${KIMIA_EXPECTED_XDG_RUNTIME_DIR}"
		test "$(stat -c "%u:%g:%a" "${XDG_RUNTIME_DIR}")" = "0:0:700"
		test "${NETAVARK_LOCK_PATH}" = "${KIMIA_EXPECTED_NETAVARK_LOCK_PATH}"
		test "$(stat -c "%u:%g:%a" "${NETAVARK_LOCK_PATH%/*}")" = "0:0:700"
		test "${CONTAINERS_STORAGE_CONF}" = "${KIMIA_EXPECTED_STORAGE_CONF}"
		grep -Eq "^[[:space:]]*driver[[:space:]]*=[[:space:]]*\"${KIMIA_EXPECTED_STORAGE_DRIVER}\"[[:space:]]*$" "${CONTAINERS_STORAGE_CONF}"
	'; then
	echo "${image}: rootful Buildah VFS runtime-directory contract is invalid" >&2
	exit 1
fi

echo "${image}: image and Harness compatibility entrypoint contracts verified"
