#!/bin/sh

set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

fail() {
	printf 'release contract failed: %s\n' "$*" >&2
	exit 1
}

assert_file() {
	[ -f "$repo_root/$1" ] || fail "missing $1"
}

assert_executable() {
	[ -x "$repo_root/$1" ] || fail "$1 is not executable"
}

assert_contains() {
	haystack=$1
	needle=$2
	case "$haystack" in
		*"$needle"*) ;;
		*) fail "expected output to contain: $needle" ;;
	esac
}

assert_count() {
	haystack=$1
	needle=$2
	want=$3
	got=$(printf '%s\n' "$haystack" | awk -v needle="$needle" 'index($0, needle) { count++ } END { print count + 0 }')
	[ "$got" -eq "$want" ] || fail "expected $want occurrences of '$needle', got $got"
}

assert_line_count() {
	haystack=$1
	line=$2
	want=$3
	got=$(printf '%s\n' "$haystack" | awk -v line="$line" '$0 == line { count++ } END { print count + 0 }')
	[ "$got" -eq "$want" ] || fail "expected $want exact lines of '$line', got $got"
}

assert_before() {
	haystack=$1
	first=$2
	second=$3
	first_line=$(printf '%s\n' "$haystack" | awk -v line="$first" '$0 == line { print NR; exit }')
	second_line=$(printf '%s\n' "$haystack" | awk -v line="$second" '$0 == line { print NR; exit }')
	[ -n "$first_line" ] || fail "missing ordered line: $first"
	[ -n "$second_line" ] || fail "missing ordered line: $second"
	[ "$first_line" -lt "$second_line" ] || fail "expected '$first' before '$second'"
}

assert_frontend_e2e_failure_blocks_build() {
	mock_dir=$(mktemp -d "${TMPDIR:-/tmp}/synchub-frontend-mock.XXXXXX")
	command_log=$mock_dir/commands.log
	cat >"$mock_dir/npm" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"$NPM_COMMAND_LOG"
if [ "$*" = 'run test:e2e' ]; then
	exit 41
fi
EOF
	chmod +x "$mock_dir/npm"

	accepted=0
	if PATH="$mock_dir:$PATH" NPM_COMMAND_LOG="$command_log" DRY_RUN=0 \
		"$repo_root/scripts/frontend.sh" >/dev/null 2>&1; then
		accepted=1
	fi
	commands=$(cat "$command_log")
	rm -f "$mock_dir/npm" "$command_log"
	rmdir "$mock_dir"

	[ "$accepted" -eq 0 ] || fail "frontend gate accepted a failing Playwright E2E run"
	assert_line_count "$commands" 'run test:e2e' 1
	assert_line_count "$commands" 'run build' 0
}

assert_clean_rejects_symlink() {
	symlink_kind=$1
	fixture_root=$(mktemp -d "${TMPDIR:-/tmp}/synchub-release-root.XXXXXX")
	external_root=$(mktemp -d "${TMPDIR:-/tmp}/synchub-release-external.XXXXXX")
	mkdir -p "$external_root/release"
	touch "$external_root/release/stale-artifact"

	case "$symlink_kind" in
		build)
			ln -s "$external_root" "$fixture_root/build"
			;;
		release)
			mkdir -p "$fixture_root/build"
			ln -s "$external_root/release" "$fixture_root/build/release"
			;;
		*) fail "unknown symlink fixture: $symlink_kind" ;;
	esac

	accepted=0
	if DRY_RUN=0 sh -c '. "$0"; repo_root=$1; prepare_clean_directory build/release' \
		"$repo_root/scripts/build-common.sh" "$fixture_root" >/dev/null 2>&1; then
		accepted=1
	fi
	marker_preserved=0
	if [ -f "$external_root/release/stale-artifact" ]; then
		marker_preserved=1
	fi

	case "$symlink_kind" in
		build)
			rm "$fixture_root/build"
			;;
		release)
			if [ -L "$fixture_root/build/release" ]; then
				rm "$fixture_root/build/release"
			else
				rmdir "$fixture_root/build/release"
			fi
			rmdir "$fixture_root/build"
			;;
	esac
	rm -f "$external_root/release/stale-artifact"
	rmdir "$external_root/release"
	rmdir "$fixture_root" "$external_root"

	[ "$accepted" -eq 0 ] || fail "clean helper accepted $symlink_kind symlink"
	[ "$marker_preserved" -eq 1 ] || fail "clean helper removed data through $symlink_kind symlink"
}

assert_file Makefile
assert_file README.md
assert_file data/config.example.yaml
assert_file scripts/build.sh
assert_file scripts/frontend.sh
assert_file scripts/release.sh
assert_file scripts/validate-config/main.go

for script in scripts/build.sh scripts/frontend.sh scripts/release.sh scripts/test-release.sh; do
	assert_executable "$script"
	sh -n "$repo_root/$script"
done

cross_build_dir=$(mktemp -d "${TMPDIR:-/tmp}/synchub-release-contract.XXXXXX")
trap 'rm -rf -- "$cross_build_dir"' EXIT HUP INT TERM
(
	cd "$repo_root"
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
		go build -trimpath -buildvcs=false -o "$cross_build_dir/sync-hub.exe" ./cmd/sync-hub
)

if grep -Fq '尚无可运行版本' "$repo_root/README.md"; then
	fail "README still says the project is not runnable"
fi
grep -Fq 'http://127.0.0.1:8888' "$repo_root/README.md" || fail "README does not document the default address"
grep -Fq 'data/config.example.yaml' "$repo_root/README.md" || fail "README does not reference the example config"

example_credentials=$(sed -nE 's/^[[:space:]]*(access_token|management_key|api_key|proxy_api_key):[[:space:]]*([^[:space:]#]+).*$/\2/p' "$repo_root/data/config.example.yaml")
proxy_api_key=$(sed -nE 's/^[[:space:]]*proxy_api_key:[[:space:]]*([^[:space:]#]+).*$/\1/p' "$repo_root/data/config.example.yaml")
[ -n "$proxy_api_key" ] || fail "example config is missing proxy_api_key"
assert_contains "$example_credentials" "$proxy_api_key"

credential_count=0
while IFS= read -r credential; do
	credential_count=$((credential_count + 1))
	case "$credential" in
		REPLACE_WITH_*) ;;
		*) fail "example credential is not an obvious placeholder" ;;
	esac
done <<EOF
$example_credentials
EOF
[ "$credential_count" -eq 6 ] || fail "expected six example credentials, got $credential_count"

validation_output=$(cd "$repo_root" && go run ./scripts/validate-config data/config.example.yaml)
assert_contains "$validation_output" 'valid config: 2 targets, 3 upstreams'

metadata_version=v1.2.3
metadata_commit=0123456789ab
metadata_date=2026-07-26T00:00:00Z

build_output=$(cd "$repo_root" && DRY_RUN=1 VERSION=$metadata_version COMMIT=$metadata_commit BUILD_DATE=$metadata_date ./scripts/build.sh)
assert_count "$build_output" 'npm ci' 1
assert_count "$build_output" 'npm run test:coverage' 1
assert_count "$build_output" 'npm run type-check' 1
assert_count "$build_output" 'npm run lint' 1
assert_count "$build_output" 'npm run test:e2e' 1
assert_count "$build_output" 'npm audit --audit-level=low' 1
assert_count "$build_output" 'npm audit --omit=dev --audit-level=low' 1
assert_count "$build_output" 'npm run build' 1
for quality_gate in \
	'+ (cd web && npm run type-check)' \
	'+ (cd web && npm run lint)' \
	'+ (cd web && npm run test:coverage)' \
	'+ (cd web && npm run test:e2e)'; do
	assert_before "$build_output" "$quality_gate" '+ (cd web && npm run build)'
done
assert_contains "$build_output" 'go test ./...'
assert_contains "$build_output" 'go test -race ./...'
assert_contains "$build_output" 'go vet ./...'
assert_contains "$build_output" 'main.version=v1.2.3'
assert_contains "$build_output" 'main.commit=0123456789ab'
assert_contains "$build_output" 'main.buildDate=2026-07-26T00:00:00Z'
assert_contains "$build_output" '-o build/sync-hub ./cmd/sync-hub'
assert_line_count "$build_output" '+ rm -rf build' 0

release_output=$(cd "$repo_root" && DRY_RUN=1 VERSION=$metadata_version COMMIT=$metadata_commit BUILD_DATE=$metadata_date ./scripts/release.sh)
assert_count "$release_output" 'npm ci' 1
assert_count "$release_output" 'npm run type-check' 1
assert_count "$release_output" 'npm run lint' 1
assert_count "$release_output" 'npm run test:coverage' 1
assert_count "$release_output" 'npm run test:e2e' 1
assert_count "$release_output" 'npm run build' 1
for quality_gate in \
	'+ (cd web && npm run type-check)' \
	'+ (cd web && npm run lint)' \
	'+ (cd web && npm run test:coverage)' \
	'+ (cd web && npm run test:e2e)'; do
	assert_before "$release_output" "$quality_gate" '+ (cd web && npm run build)'
done
assert_count "$release_output" './cmd/sync-hub' 6
assert_line_count "$release_output" '+ rm -rf build/release' 1
assert_line_count "$release_output" '+ rm -rf build' 0
for artifact in \
	sync-hub_v1.2.3_darwin_amd64 \
	sync-hub_v1.2.3_darwin_arm64 \
	sync-hub_v1.2.3_linux_amd64 \
	sync-hub_v1.2.3_linux_arm64 \
	sync-hub_v1.2.3_windows_amd64.exe \
	sync-hub_v1.2.3_windows_arm64.exe; do
	assert_contains "$release_output" "$artifact"
done

for unsafe_clean_path in '' . build /build/release ../release build/../release; do
	if DRY_RUN=1 sh -c '. "$0"; prepare_clean_directory "$1"' \
		"$repo_root/scripts/build-common.sh" "$unsafe_clean_path" >/dev/null 2>&1; then
		fail "clean helper accepted unsafe path: $unsafe_clean_path"
	fi
done
assert_clean_rejects_symlink build
assert_clean_rejects_symlink release
assert_frontend_e2e_failure_blocks_build

if (cd "$repo_root" && DRY_RUN=1 VERSION='../../escape' ./scripts/release.sh >/dev/null 2>&1); then
	fail "release accepted an unsafe version"
fi

make_output=$(cd "$repo_root" && make -n build release validate-config test-release)
assert_contains "$make_output" './scripts/build.sh'
assert_contains "$make_output" './scripts/release.sh'
assert_contains "$make_output" 'go run ./scripts/validate-config data/config.example.yaml'
assert_contains "$make_output" './scripts/test-release.sh'

printf 'release contract: PASS\n'
