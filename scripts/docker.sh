#!/bin/sh

set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH= cd -- "${script_dir}/.." && pwd)
cd "${repo_dir}"

container_cli=${KIMIA_CONTAINER_CLI:-docker}
image_arch=${KIMIA_IMAGE_ARCH:-}
if [ -z "${image_arch}" ]; then
	case "$(uname -m)" in
		x86_64 | amd64) image_arch=amd64 ;;
		aarch64 | arm64) image_arch=arm64 ;;
		*)
			echo "unsupported host architecture; set KIMIA_IMAGE_ARCH to amd64 or arm64" >&2
			exit 1
			;;
	esac
fi
case "${image_arch}" in
	amd64 | arm64) ;;
	*)
		echo "KIMIA_IMAGE_ARCH must be amd64 or arm64" >&2
		exit 1
		;;
esac

KIMIA_BUILD_ARCHES="${image_arch}" sh scripts/build.sh

for provider in docker gar ecr acr; do
	if [ "${provider}" = docker ]; then
		image=plugins/kimia
	else
		image="plugins/kimia-${provider}"
	fi
	"${container_cli}" build \
		--build-arg "PLUGIN_VERSION=${PLUGIN_VERSION:-dev}" \
		--build-arg "PLUGIN_REVISION=${PLUGIN_COMMIT:-unknown}" \
		--build-arg "PLUGIN_CREATED=${PLUGIN_CREATED:-}" \
		-f "docker/${provider}/Dockerfile.linux.${image_arch}" \
		-t "${image}:linux-${image_arch}" \
		.
	KIMIA_CONTAINER_CLI="${container_cli}" \
		PLUGIN_VERSION="${PLUGIN_VERSION:-dev}" \
		PLUGIN_COMMIT="${PLUGIN_COMMIT:-unknown}" \
		PLUGIN_CREATED="${PLUGIN_CREATED:-}" \
		sh scripts/verify-image.sh "${image}:linux-${image_arch}" "${provider}" "${image_arch}"
done

if [ "${KIMIA_SKIP_SMOKE:-false}" != true ]; then
	KIMIA_CONTAINER_CLI="${container_cli}" \
		sh scripts/smoke-image.sh "plugins/kimia:linux-${image_arch}"
fi
