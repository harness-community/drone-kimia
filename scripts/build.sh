#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH= cd -- "${script_dir}/.." && pwd)
cd "${repo_dir}"

plugin_version=${PLUGIN_VERSION:-dev}
plugin_commit=${PLUGIN_COMMIT:-unknown}
plugin_build_date=${PLUGIN_BUILD_DATE:-unknown}
build_arches=${KIMIA_BUILD_ARCHES:-"amd64 arm64"}
[ -n "${build_arches}" ] || {
	echo "KIMIA_BUILD_ARCHES must not be empty" >&2
	exit 1
}
module=github.com/harness-community/drone-kimia/internal/version
link_flags="-s -w -X ${module}.Version=${plugin_version} -X ${module}.Commit=${plugin_commit} -X ${module}.BuildDate=${plugin_build_date}"

export CGO_ENABLED=0

for arch in ${build_arches}; do
	case "${arch}" in
		amd64 | arm64) ;;
		*)
			echo "KIMIA_BUILD_ARCHES contains unsupported architecture: ${arch}" >&2
			exit 1
			;;
	esac
	output="release/linux/${arch}"
	mkdir -p "${output}"
	for provider in docker gar ecr acr; do
		GOOS=linux GOARCH="${arch}" go build \
			-buildvcs=false \
			-mod=readonly \
			-trimpath \
			-ldflags "${link_flags}" \
			-o "${output}/kimia-${provider}" \
			"./cmd/kimia-${provider}"
	done
done
