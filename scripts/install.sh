#!/bin/sh
set -eu
set -f

version=latest
profile=lite
install_dir=${HOME}/.local/bin
provider=
endpoint=
model=
api_key_stdin=0
non_interactive=0
skip_setup=0
repo=https://github.com/wenbokun434-sketch/interviewcraft
cosign_version=v3.1.3
oidc_issuer=https://token.actions.githubusercontent.com
manifest_header=interviewcraft-release-v1
receipt_header=interviewcraft-install-receipt-v1
path_begin='# >>> InterviewCraft PATH >>>'
path_end='# <<< InterviewCraft PATH <<<'
test_mode=${INTERVIEWCRAFT_INSTALL_TEST_MODE:-0}

usage() {
    printf '%s\n' 'usage: install.sh [--version VERSION] [--profile lite|private-local|full]' >&2
    printf '%s\n' '  [--install-dir DIR] [--provider openai-compatible|ollama]' >&2
    printf '%s\n' '  [--endpoint URL] [--model NAME] [--api-key-stdin] [--non-interactive] [--skip-setup]' >&2
}

while [ "$#" -gt 0 ]; do
    case "$1" in
        --version) [ "$#" -ge 2 ] || { usage; exit 2; }; version=$2; shift 2 ;;
        --profile) [ "$#" -ge 2 ] || { usage; exit 2; }; profile=$2; shift 2 ;;
        --install-dir) [ "$#" -ge 2 ] || { usage; exit 2; }; install_dir=$2; shift 2 ;;
        --provider) [ "$#" -ge 2 ] || { usage; exit 2; }; provider=$2; shift 2 ;;
        --endpoint) [ "$#" -ge 2 ] || { usage; exit 2; }; endpoint=$2; shift 2 ;;
        --model) [ "$#" -ge 2 ] || { usage; exit 2; }; model=$2; shift 2 ;;
        --api-key-stdin) api_key_stdin=1; shift ;;
        --non-interactive) non_interactive=1; shift ;;
        --skip-setup) skip_setup=1; shift ;;
        -h|--help) usage; exit 0 ;;
        *) usage; exit 2 ;;
    esac
done

case "$profile" in lite|private-local|full) ;; *) printf '%s\n' 'invalid profile' >&2; exit 2 ;; esac
case "$provider" in ''|openai-compatible|ollama) ;; *) printf '%s\n' 'invalid provider' >&2; exit 2 ;; esac
carriage_return=$(printf '\r')
case "$install_dir" in *'|'*|*"$carriage_return"*|*"	"*|*"
"*) printf '%s\n' 'install directory contains a control character or reserved delimiter' >&2; exit 2 ;; esac
case "$HOME" in *'|'*|*"$carriage_return"*|*"	"*|*"
"*) printf '%s\n' 'home directory contains a control character or reserved delimiter' >&2; exit 2 ;; esac

stage() { printf '[%s/7] %s\n' "$1" "$2"; }
die() { printf 'install: %s\n' "$1" >&2; exit 1; }

download() {
    source_uri=$1
    target_path=$2
    rm -f "$target_path"
    case "$source_uri" in
        file:///*)
            [ "$test_mode" = 1 ] || die 'file release fixtures are only available in installer test mode'
            cp "${source_uri#file://}" "$target_path" || die "fixture copy failed: $source_uri"
            ;;
        *) if command -v curl >/dev/null 2>&1; then
        curl -fL --retry 2 --connect-timeout 20 --output "$target_path" "$source_uri" || die "download failed: $source_uri; check network/proxy settings"
        elif command -v wget >/dev/null 2>&1; then
        wget -O "$target_path" "$source_uri" || die "download failed: $source_uri; check network/proxy settings"
        else
        die 'curl or wget is required for verified downloads'
        fi ;;
    esac
    [ -s "$target_path" ] || die "download was empty: $source_uri"
}

sha256_file() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    elif command -v shasum >/dev/null 2>&1; then
        shasum -a 256 "$1" | awk '{print $1}'
    else
        die 'sha256sum or shasum is required'
    fi
}

case "$(uname -s)" in
    Linux) goos=linux ;;
    Darwin) goos=darwin ;;
    *) die 'unsupported operating system; use install.ps1 on Windows' ;;
esac
case "$(uname -m)" in
    x86_64|amd64) goarch=amd64 ;;
    arm64|aarch64) goarch=arm64 ;;
    *) die 'unsupported architecture; InterviewCraft requires amd64 or arm64' ;;
esac

case "$version" in
    latest)
        [ "$test_mode" = 1 ] && die 'test fixture must request an explicit version'
        command -v curl >/dev/null 2>&1 || die 'curl is required to resolve the latest release'
        effective=$(curl -fsSL -o /dev/null -w '%{url_effective}' "$repo/releases/latest") || die 'could not resolve latest release'
        tag=${effective##*/}
        version=${tag#v}
        ;;
    v*) version=${version#v}; tag=v$version ;;
    *) tag=v$version ;;
esac
printf '%s\n' "$version" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z][0-9A-Za-z.-]*)?$' || die 'version must be latest or a semantic version'
tag=v$version

case "$install_dir" in
    /*) ;;
    *) install_dir=$(pwd)/$install_dir ;;
esac
case "/$install_dir/" in *'/../'*|*'/./'*) die 'install directory must not contain . or .. segments' ;; esac
binary_path=$install_dir/interviewcraft
receipt_path=${INTERVIEWCRAFT_INSTALL_TEST_RECEIPT:-$HOME/.interviewcraft/install-receipt.txt}
if [ "$test_mode" != 1 ]; then receipt_path=$HOME/.interviewcraft/install-receipt.txt; fi
data_dir=${INTERVIEWCRAFT_DATA_DIR:-$HOME/.interviewcraft}
case "$data_dir" in /*) ;; *) data_dir=$(pwd)/$data_dir ;; esac

installed_version=
if [ -f "$binary_path" ]; then
    installed_json=$($binary_path version --json 2>/dev/null) || die 'existing interviewcraft binary is unreadable; move it aside manually'
    installed_version=$(printf '%s' "$installed_json" | sed -n 's/.*"version":"\([^"]*\)".*/\1/p')
    [ -n "$installed_version" ] || die 'existing interviewcraft binary returned invalid version JSON'
    if [ "$installed_version" != "$version" ]; then
        INTERVIEWCRAFT_INSTALL_RECEIPT=$receipt_path "$binary_path" update --version "$version" || die 'verified update failed; the previous binary and matching data were restored'
        printf 'InterviewCraft update from %s to %s was accepted by the verified updater.\n' "$installed_version" "$version"
        exit 0
    fi
fi

temp_root=$(mktemp -d "${TMPDIR:-/tmp}/interviewcraft-install.XXXXXX") || die 'could not create a private temporary directory'
case "$temp_root" in ${TMPDIR:-/tmp}/interviewcraft-install.*) ;; *) die 'invalid temporary directory' ;; esac
installed_new=0
temporary_binary=
path_files=
cleanup() {
    status=$?
    trap - EXIT HUP INT TERM
    if [ "$status" -ne 0 ] && [ "$installed_new" = 1 ]; then rm -f "$binary_path"; fi
    [ -z "$temporary_binary" ] || rm -f "$temporary_binary"
    if [ "$status" -ne 0 ] && [ -n "$path_files" ]; then
        old_cleanup_ifs=$IFS
        IFS='|'
        for cleanup_path in $path_files; do
            cleanup_tmp=$cleanup_path.interviewcraft-cleanup-$$.tmp
            strip_block "$cleanup_path" "$cleanup_tmp"
            mv "$cleanup_tmp" "$cleanup_path"
        done
        IFS=$old_cleanup_ifs
    fi
    rm -rf "$temp_root"
    exit "$status"
}
trap cleanup EXIT HUP INT TERM

release_base=$repo/releases/download
if [ "$test_mode" = 1 ]; then
    release_base=${INTERVIEWCRAFT_INSTALL_TEST_RELEASE_BASE_URL:-}
    case "$release_base" in http://127.0.0.1:*|http://localhost:*|http://\[::1\]:*|file:///*) ;; *) die 'test release fixture must use a loopback URL or absolute file fixture' ;; esac
fi
tag_base=${release_base%/}/$tag
manifest_path=$temp_root/release-manifest.txt
bundle_path=$temp_root/release-manifest.sigstore.json

stage 1 'downloading signed release metadata'
download "$tag_base/release-manifest.txt" "$manifest_path"
download "$tag_base/release-manifest.sigstore.json" "$bundle_path"

stage 2 'preparing pinned Cosign verifier'
if [ "$test_mode" = 1 ]; then
    cosign_path=${INTERVIEWCRAFT_INSTALL_TEST_COSIGN_PATH:-}
    expected_cosign=${INTERVIEWCRAFT_INSTALL_TEST_COSIGN_SHA256:-}
    case "$cosign_path" in ${TMPDIR:-/tmp}/*) ;; *) die 'test Cosign fixture must be under the temporary directory' ;; esac
else
    case "$goos/$goarch" in
        darwin/amd64) cosign_name=cosign-darwin-amd64; expected_cosign=2347488e5d5b25336644024dfeca5601b190e91197a71a917bda44744aff106c ;;
        darwin/arm64) cosign_name=cosign-darwin-arm64; expected_cosign=5cf948c2f4dfe59687bdd0b8523709067383e03982cc543475c8a7dc70e92a76 ;;
        linux/amd64) cosign_name=cosign-linux-amd64; expected_cosign=4629c757b7618056f8ddd7e2625ae9fdd94c0372a65049520bc7d9df9efc7f71 ;;
        linux/arm64) cosign_name=cosign-linux-arm64; expected_cosign=c5d324e091826b0d7a78eb16fef316450b4eb9aaec045611c08ba06f5e73220a ;;
    esac
    cosign_path=$temp_root/$cosign_name
    download "https://github.com/sigstore/cosign/releases/download/$cosign_version/$cosign_name" "$cosign_path"
    chmod 700 "$cosign_path"
fi
[ "$(sha256_file "$cosign_path")" = "$expected_cosign" ] || die 'Cosign verifier hash does not match the repository-pinned matrix'

stage 3 'verifying manifest signature and publisher identity'
identity=https://github.com/wenbokun434-sketch/interviewcraft/.github/workflows/release.yml@refs/tags/$tag
"$cosign_path" verify-blob --bundle "$bundle_path" --certificate-identity "$identity" --certificate-oidc-issuer "$oidc_issuer" "$manifest_path" || die 'release manifest signature or publisher identity is invalid'

tab=$(printf '\tX'); tab=${tab%X}
meta_seen=0
checksum_seen=0
sbom_count=0
asset_count=0
seen_platforms='|'
seen_files='|'
selected_filename=
selected_sha=
selected_size=
line_number=0
while IFS= read -r line || [ -n "$line" ]; do
    line_number=$((line_number + 1))
    if [ "$line_number" -eq 1 ]; then [ "$line" = "$manifest_header" ] || die 'unsupported manifest header'; continue; fi
    [ -n "$line" ] || die "manifest line $line_number is blank"
    old_ifs=$IFS; IFS=$tab
    # shellcheck disable=SC2086 # Intentional strict Tab field splitting; globbing is disabled by set -f.
    set -- $line
    IFS=$old_ifs
    [ "$#" -eq 4 ] || [ "$#" -eq 6 ] || die "manifest line $line_number has an invalid field count"
    kind=$1
    case "$kind" in
        meta)
            if ! { [ "$#" -eq 4 ] && [ "$meta_seen" -eq 0 ] && [ "$line_number" -eq 2 ]; }; then die 'invalid or duplicate manifest meta row'; fi
            [ "$2" = "$version" ] || die 'manifest version does not match requested version'
            printf '%s\n' "$3" | grep -Eq '^[0-9a-f]{7,64}$' || die 'manifest commit is invalid'
            printf '%s\n' "$4" | grep -Eq '^[0-9]{4}-[0-9]{2}-[0-9]{2}T.*Z$' || die 'manifest UTC timestamp is invalid'
            meta_seen=1
            continue
            ;;
        asset)
            if ! { [ "$#" -eq 6 ] && [ "$meta_seen" -eq 1 ] && [ "$checksum_seen" -eq 0 ]; }; then die 'invalid manifest asset row'; fi
            platform=$2/$3
            case "$platform" in darwin/amd64|darwin/arm64|linux/amd64|linux/arm64|windows/amd64|windows/arm64) ;; *) die 'unsupported manifest platform' ;; esac
            case "$seen_platforms" in *"|$platform|"*) die 'duplicate manifest platform' ;; esac
            seen_platforms=$seen_platforms$platform'|'
            asset_count=$((asset_count + 1))
            ;;
        checksum)
            if ! { [ "$#" -eq 6 ] && [ "$meta_seen" -eq 1 ] && [ "$checksum_seen" -eq 0 ] && [ "$2" = - ] && [ "$3" = - ] && [ "$4" = checksums.txt ]; }; then die 'invalid manifest checksum row'; fi
            checksum_seen=1
            ;;
        sbom)
            if ! { [ "$#" -eq 6 ] && [ "$checksum_seen" -eq 1 ] && [ "$2" = - ] && [ "$3" = - ]; }; then die 'invalid manifest SBOM row'; fi
            case "$4" in *.spdx.json) ;; *) die 'invalid manifest SBOM filename' ;; esac
            sbom_count=$((sbom_count + 1))
            ;;
        *) die 'manifest contains an unknown row kind' ;;
    esac
    filename=$4; hash=$5; size=$6
    case "$filename" in ''|.|..|/*|*/*|*\\*|*[!A-Za-z0-9._-]*) die 'manifest contains an invalid filename' ;; esac
    case "$seen_files" in *"|$filename|"*) die 'manifest contains a duplicate filename' ;; esac
    seen_files=$seen_files$filename'|'
    [ "${#hash}" -eq 64 ] || die 'manifest SHA-256 length is invalid'
    case "$hash" in *[!0-9a-f]*) die 'manifest SHA-256 must be lowercase hexadecimal' ;; esac
    case "$size" in ''|0|*[!0-9]*) die 'manifest size must be positive base-10' ;; esac
    if [ "$kind" = asset ] && [ "$2" = "$goos" ] && [ "$3" = "$goarch" ]; then
        selected_filename=$filename; selected_sha=$hash; selected_size=$size
    fi
done < "$manifest_path"
if ! { [ "$asset_count" -eq 6 ] && [ "$checksum_seen" -eq 1 ] && [ "$sbom_count" -ge 1 ]; }; then die 'release manifest is incomplete'; fi
for required in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
    case "$seen_platforms" in *"|$required|"*) ;; *) die "manifest is missing $required" ;; esac
done
[ -n "$selected_filename" ] || die 'manifest has no asset for this platform'

stage 4 "downloading and hashing $selected_filename"
archive_path=$temp_root/$selected_filename
download "$tag_base/$selected_filename" "$archive_path"
actual_size=$(wc -c < "$archive_path" | tr -d ' ')
if ! { [ "$actual_size" = "$selected_size" ] && [ "$(sha256_file "$archive_path")" = "$selected_sha" ]; }; then die 'application archive hash or size does not match the signed manifest'; fi
available_bytes=${INTERVIEWCRAFT_INSTALL_TEST_FREE_BYTES:-}
if [ -z "$available_bytes" ] || [ "$test_mode" != 1 ]; then
    check_dir=$(dirname "$install_dir")
    [ -d "$check_dir" ] || check_dir=$HOME
    available_kb=$(df -Pk "$check_dir" | awk 'NR==2 {print $4}')
    available_bytes=$((available_kb * 1024))
fi
[ "$available_bytes" -ge $((selected_size * 3)) ] || die 'insufficient disk space for verified extraction'

stage 5 'validating archive paths and embedded version'
extract_root=$temp_root/extract
mkdir "$extract_root"
list_path=$temp_root/archive-list.txt
tar -tzf "$archive_path" > "$list_path" || die 'application archive is truncated or invalid'
binary_entries=0
seen_archive_entries='|'
while IFS= read -r entry; do
    case "$entry" in /*|*\\*|../*|*/../*|*/..|.|./*) die 'archive contains an absolute or traversal path' ;; esac
    case "$seen_archive_entries" in *"|$entry|"*) die "archive contains a duplicate entry: $entry" ;; esac
    seen_archive_entries=$seen_archive_entries$entry'|'
    case "$entry" in
        interviewcraft) binary_entries=$((binary_entries + 1)) ;;
        README.md|docs/|docs/DEPLOYMENT.md|docs/SECURITY.md|scripts/|scripts/install.ps1|scripts/install.sh|scripts/uninstall.ps1|scripts/uninstall.sh|scripts/cosign-v3.1.3-sha256.txt) ;;
        *) die "archive contains an unexpected file or executable: $entry" ;;
    esac
done < "$list_path"
[ "$binary_entries" -eq 1 ] || die 'archive must contain exactly one interviewcraft executable'
tar -tvzf "$archive_path" | while IFS= read -r listing; do
    type=$(printf '%.1s' "$listing")
    case "$type" in -|d) ;; *) die 'archive contains a symbolic link or unsupported entry type' ;; esac
done
tar -xzf "$archive_path" -C "$extract_root" || die 'verified archive extraction failed'
[ -z "$(find "$extract_root" -type l -print -quit)" ] || die 'archive extracted a symbolic link'
staged_binary=$extract_root/interviewcraft
chmod 700 "$staged_binary"
staged_json=$($staged_binary version --json) || die 'archive binary version check failed'
staged_version=$(printf '%s' "$staged_json" | sed -n 's/.*"version":"\([^"]*\)".*/\1/p')
staged_goos=$(printf '%s' "$staged_json" | sed -n 's/.*"goos":"\([^"]*\)".*/\1/p')
staged_goarch=$(printf '%s' "$staged_json" | sed -n 's/.*"goarch":"\([^"]*\)".*/\1/p')
if ! { [ "$staged_version" = "$version" ] && [ "$staged_goos" = "$goos" ] && [ "$staged_goarch" = "$goarch" ]; }; then die 'archive binary version or platform does not match the signed manifest'; fi

stage 6 'installing and completing setup/doctor'
if [ -z "$installed_version" ]; then
    mkdir -p "$install_dir" || die 'install directory is not writable'
    temporary_binary=$(mktemp "$install_dir/.interviewcraft.XXXXXX") || die 'could not create a temporary binary in the install directory'
    cp "$staged_binary" "$temporary_binary" || die 'could not stage binary in install directory'
    chmod 755 "$temporary_binary"
    installed_new=1
    mv "$temporary_binary" "$binary_path" || die 'could not atomically install binary'
    temporary_binary=
fi
if [ "$skip_setup" -eq 0 ]; then
    set -- setup --profile "$profile"
    [ -z "$provider" ] || set -- "$@" --provider "$provider"
    [ -z "$endpoint" ] || set -- "$@" --endpoint "$endpoint"
    [ -z "$model" ] || set -- "$@" --model "$model"
    [ "$api_key_stdin" -eq 0 ] || set -- "$@" --api-key-stdin
    [ "$non_interactive" -eq 0 ] || set -- "$@" --non-interactive
    "$binary_path" "$@" || die 'setup failed; fix the reported dependency and rerun the installer'
    "$binary_path" doctor || die 'doctor failed; fix the reported dependency and rerun the installer'
fi

strip_block() {
    input_file=$1; output_file=$2
    awk -v begin="$path_begin" -v end="$path_end" '
        $0 == begin { inside=1; next }
        $0 == end { inside=0; next }
        !inside { print }
    ' "$input_file" > "$output_file"
}

shell_quote=$(printf '%s' "$install_dir" | sed "s/'/'\\\\''/g")
add_path_file() {
    target_file=$1; shell_kind=$2
    mkdir -p "$(dirname "$target_file")"
    [ -f "$target_file" ] || : > "$target_file"
    managed_tmp=$target_file.interviewcraft-$$.tmp
    strip_block "$target_file" "$managed_tmp"
    printf '\n%s\n' "$path_begin" >> "$managed_tmp"
    if [ "$shell_kind" = fish ]; then
        printf "if not contains -- '%s' \$PATH\n    set -gx PATH '%s' \$PATH\nend\n" "$shell_quote" "$shell_quote" >> "$managed_tmp"
    else
        printf "case \":\$PATH:\" in *\":%s:\"*) ;; *) export PATH='%s':\"\$PATH\" ;; esac\n" "$shell_quote" "$shell_quote" >> "$managed_tmp"
    fi
    printf '%s\n' "$path_end" >> "$managed_tmp"
    mv "$managed_tmp" "$target_file"
    case "|$path_files|" in *"|$target_file|"*) ;; *) path_files=${path_files}${path_files:+|}$target_file ;; esac
}

stage 7 'managing user PATH and writing receipt'
add_path_file "$HOME/.profile" posix
[ ! -f "$HOME/.bashrc" ] || add_path_file "$HOME/.bashrc" posix
[ ! -f "$HOME/.zshrc" ] || add_path_file "$HOME/.zshrc" posix
[ ! -f "$HOME/.config/fish/config.fish" ] || add_path_file "$HOME/.config/fish/config.fish" fish
case "${SHELL:-}" in */bash) add_path_file "$HOME/.bashrc" posix ;; */zsh) add_path_file "$HOME/.zshrc" posix ;; */fish) add_path_file "$HOME/.config/fish/config.fish" fish ;; esac

mkdir -p "$(dirname "$receipt_path")"
receipt_tmp=$(dirname "$receipt_path")/.install-receipt-$$.tmp
{
    printf '%s\n' "$receipt_header"
    printf 'version\t%s\ninstall_dir\t%s\nbinary_path\t%s\ndata_dir\t%s\n' "$version" "$install_dir" "$binary_path" "$data_dir"
    old_ifs=$IFS; IFS='|'; for path_file in $path_files; do printf 'path_file\t%s\n' "$path_file"; done; IFS=$old_ifs
} > "$receipt_tmp"
mv "$receipt_tmp" "$receipt_path"
installed_new=0
trap - EXIT HUP INT TERM
rm -rf "$temp_root"
printf 'InterviewCraft %s installed at %s\n' "$version" "$binary_path"
printf '%s\n' 'Open a new terminal and run: interviewcraft version'
