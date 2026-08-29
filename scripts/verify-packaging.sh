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
	KIMIA_VERSION KIMIA_SOURCE_COMMIT KIMIA_INDEX_DIGEST \
	KIMIA_AMD64_DIGEST KIMIA_ARM64_DIGEST KIMIA_BUILDKIT_VERSION \
	KIMIA_ROOTLESSKIT_VERSION GO_IMAGE DOCKER_PLUGIN_IMAGE \
	MANIFEST_PLUGIN_IMAGE; do
	require_value "${name}"
done

case "${KIMIA_VERSION}" in
	[0-9]*.[0-9]*.[0-9]*) ;;
	*) fail "KIMIA_VERSION must be a semantic version without a v prefix" ;;
esac

[ "${#KIMIA_SOURCE_COMMIT}" -eq 40 ] || fail "KIMIA_SOURCE_COMMIT must contain 40 hexadecimal characters"
case "${KIMIA_SOURCE_COMMIT}" in
	*[!0-9a-f]*) fail "KIMIA_SOURCE_COMMIT must be lowercase hexadecimal" ;;
esac

for name in KIMIA_INDEX_DIGEST KIMIA_AMD64_DIGEST KIMIA_ARM64_DIGEST; do
	validate_digest "${name}"
done

for image in "${GO_IMAGE}" "${DOCKER_PLUGIN_IMAGE}" "${MANIFEST_PLUGIN_IMAGE}"; do
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
		require_contains "${dockerfile}" "FROM ghcr.io/rapidfort/kimia:${KIMIA_VERSION}@${digest}"
		require_contains "${dockerfile}" "ENV KIMIA_VERSION=${KIMIA_VERSION}"
		require_contains "${dockerfile}" "org.opencontainers.image.title=\"${title}\""
		require_contains "${dockerfile}" 'org.opencontainers.image.source="https://github.com/harness-community/drone-kimia"'
		require_contains "${dockerfile}" 'org.opencontainers.image.version="${PLUGIN_VERSION}"'
		require_contains "${dockerfile}" 'org.opencontainers.image.revision="${PLUGIN_REVISION}"'
		require_contains "${dockerfile}" "org.opencontainers.image.base.digest=\"${digest}\""
		require_contains "${dockerfile}" "COPY release/linux/${arch}/kimia-${provider} /usr/local/bin/kimia-${provider}"
		require_contains "${dockerfile}" "RUN test \"\$(id -u):\$(id -g)\" = \"1000:1000\""
		require_contains "${dockerfile}" "--help | grep -q \"PLUGIN_EXPAND_TAG\""
		require_contains "${dockerfile}" "--help | grep -q \"${auth_help_input}\""
		require_contains "${dockerfile}" "ENTRYPOINT [\"/usr/local/bin/kimia-${provider}\"]"
		require_contains "${dockerfile}" "CMD []"
		if grep -Eq '^USER[[:space:]]+' "${dockerfile}"; then
			grep -Eq '^USER[[:space:]]+1000(:1000)?$' "${dockerfile}" || fail "${dockerfile} overrides the upstream non-root user"
		fi
	done
done

require_contains .drone.yml "image: ${GO_IMAGE}"
require_contains .drone.yml "image: ${DOCKER_PLUGIN_IMAGE}"
require_contains .drone.yml "image: ${MANIFEST_PLUGIN_IMAGE}"
created_build_args=$(grep -c 'PLUGIN_CREATED=${DRONE_BUILD_CREATED:-unknown}' .drone.yml)
[ "${created_build_args}" -eq 8 ] || fail ".drone.yml must pass PLUGIN_CREATED to all eight image builds"

for harness_file in \
	.harness/harness.yaml \
	.harness/eventPR.yaml \
	.harness/eventPush.yaml \
	.harness/eventTag.yaml; do
	[ -f "${harness_file}" ] || fail "missing ${harness_file}"
done

require_contains .harness/harness.yaml "identifier: dronekimiaharness"
require_contains .harness/harness.yaml "repoName: drone-kimia"
require_contains .harness/harness.yaml "image: ${GO_IMAGE}"
require_contains .harness/harness.yaml "image: ${DOCKER_PLUGIN_IMAGE}"
require_contains .harness/harness.yaml "image: ${MANIFEST_PLUGIN_IMAGE}"
require_contains .harness/harness.yaml 'repo: plugins/kimia<+matrix.image>'
require_contains .harness/harness.yaml 'spec: docker/<+matrix.repo>/manifest.tmpl'
require_contains .harness/harness.yaml 'password: <+secrets.getValue("Plugins_Docker_Hub_Pat")>'
require_contains .harness/harness.yaml 'PLUGIN_VERSION: <+codebase.tag>'
require_contains .harness/harness.yaml 'PLUGIN_COMMIT: <+codebase.commitSha>'
require_contains .harness/harness.yaml 'PLUGIN_BUILD_DATE: <+pipeline.startTs>'
require_contains .harness/harness.yaml 'PLUGIN_VERSION=<+codebase.tag>'
require_contains .harness/harness.yaml 'PLUGIN_REVISION=<+codebase.commitSha>'
require_contains .harness/harness.yaml 'PLUGIN_CREATED=<+pipeline.startTs>'
require_contains .harness/harness.yaml 'ignore_missing: "false"'

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

[ "$(harness_matrix_value_total image)" -eq 16 ] || fail ".harness/harness.yaml must have four image suffixes in each of four publish matrices"
for image_suffix in "" -gar -ecr -acr; do
	[ "$(harness_matrix_value_count image "${image_suffix}")" -eq 4 ] \
		|| fail ".harness/harness.yaml must include image suffix '${image_suffix}' exactly four times"
done

[ "$(harness_matrix_value_total repo)" -eq 20 ] || fail ".harness/harness.yaml must have four provider directories in four publish matrices and the manifest matrix"
for provider_directory in docker gar ecr acr; do
	[ "$(harness_matrix_value_count repo "${provider_directory}")" -eq 5 ] \
		|| fail ".harness/harness.yaml must include provider directory '${provider_directory}' exactly five times"
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
