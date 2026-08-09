#!/bin/sh
set -eu
set -f

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
go_binary=${GO_BINARY:-go}
version=1.2.3
tag=v$version
commit=0123456789abcdef0123456789abcdef01234567
created_utc=2026-08-10T12:00:00Z
fixture_root=$(mktemp -d "${TMPDIR:-/tmp}/interviewcraft-posix-fixture.XXXXXX")

cleanup() {
    status=$?
    trap - EXIT HUP INT TERM
    case "$fixture_root" in ${TMPDIR:-/tmp}/interviewcraft-posix-fixture.*) rm -rf "$fixture_root" ;; esac
    exit "$status"
}
trap cleanup EXIT HUP INT TERM

fail() { printf 'posix installer fixture: %s\n' "$1" >&2; exit 1; }
sha256_file() {
    if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
    else shasum -a 256 "$1" | awk '{print $1}'
    fi
}
file_size() { wc -c < "$1" | tr -d ' '; }

case "$(uname -s)" in Linux) goos=linux ;; Darwin) goos=darwin ;; *) fail 'unsupported fixture OS' ;; esac
case "$(uname -m)" in x86_64|amd64) goarch=amd64 ;; arm64|aarch64) goarch=arm64 ;; *) fail 'unsupported fixture architecture' ;; esac

release_root=$fixture_root/release
release_dir=$release_root/$tag
source_dir=$fixture_root/source
home_dir=$fixture_root/home
install_dir=$fixture_root/install/bin
mkdir -p "$release_dir" "$source_dir" "$home_dir" "$fixture_root/tmp"

ldflags="-X github.com/interviewcraft/interviewcraft/internal/version.ApplicationVersion=$version -X github.com/interviewcraft/interviewcraft/internal/version.GitCommit=$commit -X github.com/interviewcraft/interviewcraft/internal/version.BuildTime=$created_utc"
(
    cd "$repo_root"
    CGO_ENABLED=0 GOOS=$goos GOARCH=$goarch "$go_binary" build -buildvcs=false -trimpath -ldflags "$ldflags" -o "$source_dir/interviewcraft" ./cmd/interviewcraft
)
chmod 755 "$source_dir/interviewcraft"

for os_name in darwin linux windows; do
    for arch_name in amd64 arm64; do
        if [ "$os_name" = windows ]; then extension=.zip; else extension=.tar.gz; fi
        printf 'fixture %s/%s\n' "$os_name" "$arch_name" > "$release_dir/interviewcraft_${version}_${os_name}_${arch_name}${extension}"
    done
done
archive=$release_dir/interviewcraft_${version}_${goos}_${goarch}.tar.gz
tar -czf "$archive" -C "$source_dir" interviewcraft
printf '%s\n' 'fixture checksums' > "$release_dir/checksums.txt"
printf '%s\n' '{"spdxVersion":"SPDX-2.3"}' > "$release_dir/interviewcraft_${version}.spdx.json"
printf '%s\n' 'VALID FIXTURE BUNDLE' > "$release_dir/release-manifest.sigstore.json"

write_manifest() {
    manifest=$release_dir/release-manifest.txt
    {
        printf '%s\n' 'interviewcraft-release-v1'
        printf 'meta\t%s\t%s\t%s\n' "$version" "$commit" "$created_utc"
        for os_name in darwin linux windows; do
            for arch_name in amd64 arm64; do
                if [ "$os_name" = windows ]; then extension=.zip; else extension=.tar.gz; fi
                filename=interviewcraft_${version}_${os_name}_${arch_name}${extension}
                path=$release_dir/$filename
                printf 'asset\t%s\t%s\t%s\t%s\t%s\n' "$os_name" "$arch_name" "$filename" "$(sha256_file "$path")" "$(file_size "$path")"
            done
        done
        path=$release_dir/checksums.txt
        printf 'checksum\t-\t-\tchecksums.txt\t%s\t%s\n' "$(sha256_file "$path")" "$(file_size "$path")"
        filename=interviewcraft_${version}.spdx.json
        path=$release_dir/$filename
        printf 'sbom\t-\t-\t%s\t%s\t%s\n' "$filename" "$(sha256_file "$path")" "$(file_size "$path")"
    } > "$manifest"
}
write_manifest

fake_cosign=$fixture_root/tmp/cosign-fixture
{
    printf '%s\n' '#!/bin/sh'
    # shellcheck disable=SC2016 # The generated fixture must inspect its own third argument.
    printf '%s\n' 'grep -q '\''VALID FIXTURE BUNDLE'\'' "$3"'
} > "$fake_cosign"
chmod 700 "$fake_cosign"

HOME=$home_dir
TMPDIR=$fixture_root/tmp
export HOME TMPDIR
export SHELL=/bin/sh
export INTERVIEWCRAFT_INSTALL_TEST_MODE=1
INTERVIEWCRAFT_INSTALL_TEST_RELEASE_BASE_URL=file://$release_root
INTERVIEWCRAFT_INSTALL_TEST_COSIGN_PATH=$fake_cosign
INTERVIEWCRAFT_INSTALL_TEST_COSIGN_SHA256=$(sha256_file "$fake_cosign")
export INTERVIEWCRAFT_INSTALL_TEST_RELEASE_BASE_URL INTERVIEWCRAFT_INSTALL_TEST_COSIGN_PATH INTERVIEWCRAFT_INSTALL_TEST_COSIGN_SHA256
export INTERVIEWCRAFT_INSTALL_TEST_FREE_BYTES=999999999

sh "$repo_root/scripts/install.sh" --version "$version" --profile lite --install-dir "$install_dir" --skip-setup
sh "$repo_root/scripts/install.sh" --version "$version" --profile lite --install-dir "$install_dir" --skip-setup
[ -x "$install_dir/interviewcraft" ] || fail 'main install did not create the binary'
[ "$(grep -c '^# >>> InterviewCraft PATH >>>$' "$HOME/.profile")" -eq 1 ] || fail 'PATH block is not idempotent'
if sh "$repo_root/scripts/install.sh" --version 9.9.9 --install-dir "$install_dir" --skip-setup >/dev/null 2>&1; then fail 'different version overwrite was accepted'; fi

printf '%s\n' 'preserve me' > "$HOME/.interviewcraft/keep.txt"
sh "$repo_root/scripts/uninstall.sh"
[ ! -e "$install_dir/interviewcraft" ] || fail 'uninstall left the binary behind'
[ -f "$HOME/.interviewcraft/keep.txt" ] || fail 'uninstall removed user data'
if grep -q 'InterviewCraft PATH' "$HOME/.profile"; then fail 'uninstall left a PATH marker'; fi

valid_manifest=$fixture_root/valid-manifest.txt
cp "$release_dir/release-manifest.txt" "$valid_manifest"
tab=$(printf '\tX'); tab=${tab%X}
grep -v "^asset${tab}${goos}${tab}${goarch}${tab}" "$valid_manifest" > "$release_dir/release-manifest.txt"
if sh "$repo_root/scripts/install.sh" --version "$version" --install-dir "$install_dir" --skip-setup >/dev/null 2>&1; then fail 'missing platform manifest was accepted'; fi
cp "$valid_manifest" "$release_dir/release-manifest.txt"

printf '%s\n' 'INVALID BUNDLE' > "$release_dir/release-manifest.sigstore.json"
if sh "$repo_root/scripts/install.sh" --version "$version" --install-dir "$install_dir" --skip-setup >/dev/null 2>&1; then fail 'invalid signature was accepted'; fi
printf '%s\n' 'VALID FIXTURE BUNDLE' > "$release_dir/release-manifest.sigstore.json"

printf '%s\n' 'tamper' >> "$archive"
if sh "$repo_root/scripts/install.sh" --version "$version" --install-dir "$install_dir" --skip-setup >/dev/null 2>&1; then fail 'tampered archive was accepted'; fi
tar -czf "$archive" -C "$source_dir" interviewcraft
write_manifest

printf '%s\n' 'signed but truncated' > "$archive"
write_manifest
if sh "$repo_root/scripts/install.sh" --version "$version" --install-dir "$install_dir" --skip-setup >/dev/null 2>&1; then fail 'truncated archive was accepted'; fi
tar -czf "$archive" -C "$source_dir" interviewcraft
write_manifest

export INTERVIEWCRAFT_INSTALL_TEST_FREE_BYTES=0
if sh "$repo_root/scripts/install.sh" --version "$version" --install-dir "$install_dir" --skip-setup >/dev/null 2>&1; then fail 'insufficient disk space was accepted'; fi
export INTERVIEWCRAFT_INSTALL_TEST_FREE_BYTES=999999999

rm -f "$HOME/.profile"
mkdir "$HOME/.profile"
if sh "$repo_root/scripts/install.sh" --version "$version" --install-dir "$install_dir" --skip-setup >/dev/null 2>&1; then fail 'PATH write failure was accepted'; fi
[ ! -e "$install_dir/interviewcraft" ] || fail 'PATH failure left the binary behind'
rmdir "$HOME/.profile"

printf '%s\n' 'InterviewCraft POSIX installer fixture passed.'
