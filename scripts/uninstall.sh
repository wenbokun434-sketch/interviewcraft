#!/bin/sh
set -eu

receipt_path=${INTERVIEWCRAFT_INSTALL_TEST_RECEIPT:-$HOME/.interviewcraft/install-receipt.txt}
purge_data=0
confirm_purge=
while [ "$#" -gt 0 ]; do
    case "$1" in
        --receipt) [ "$#" -ge 2 ] || exit 2; receipt_path=$2; shift 2 ;;
        --purge-data) purge_data=1; shift ;;
        --confirm-purge) [ "$#" -ge 2 ] || exit 2; confirm_purge=$2; shift 2 ;;
        -h|--help) printf '%s\n' 'usage: uninstall.sh [--receipt PATH] [--purge-data --confirm-purge EXACT_DATA_DIR]'; exit 0 ;;
        *) printf '%s\n' 'uninstall: invalid option' >&2; exit 2 ;;
    esac
done
header=interviewcraft-install-receipt-v1
path_begin='# >>> InterviewCraft PATH >>>'
path_end='# <<< InterviewCraft PATH <<<'
[ -f "$receipt_path" ] || { printf 'uninstall: install receipt not found: %s\n' "$receipt_path" >&2; exit 1; }

first=$(sed -n '1p' "$receipt_path")
[ "$first" = "$header" ] || { printf '%s\n' 'uninstall: invalid install receipt' >&2; exit 1; }
tab=$(printf '\tX'); tab=${tab%X}
version=
install_dir=
binary_path=
path_files=
data_dir=
while IFS=$tab read -r key value extra; do
    [ -z "${extra:-}" ] || { printf '%s\n' 'uninstall: malformed receipt' >&2; exit 1; }
    case "$key" in
        "$header") ;;
        version) [ -z "$version" ] || exit 1; version=$value ;;
        install_dir) [ -z "$install_dir" ] || exit 1; install_dir=$value ;;
        binary_path) [ -z "$binary_path" ] || exit 1; binary_path=$value ;;
        data_dir) [ -z "$data_dir" ] || exit 1; data_dir=$value ;;
        path_file) path_files=${path_files}${path_files:+|}$value ;;
        *) printf 'uninstall: unknown receipt field: %s\n' "$key" >&2; exit 1 ;;
    esac
done < "$receipt_path"
if ! { [ -n "$version" ] && [ -n "$install_dir" ] && [ -n "$binary_path" ]; }; then printf '%s\n' 'uninstall: incomplete receipt' >&2; exit 1; fi
if ! { [ "$(dirname "$binary_path")" = "$install_dir" ] && [ "$(basename "$binary_path")" = interviewcraft ]; }; then printf '%s\n' 'uninstall: unsafe binary path in receipt' >&2; exit 1; fi

if [ "$purge_data" -eq 1 ]; then
    [ -n "$data_dir" ] || { printf '%s\n' 'uninstall: purge requires a receipt-bound data directory; run a verified update first' >&2; exit 1; }
    "$binary_path" uninstall --purge-data --confirm-purge "$confirm_purge" || { printf '%s\n' 'uninstall: safe purge was rejected; no broad fallback deletion was attempted' >&2; exit 1; }
    exit 0
fi

strip_block() {
    target=$1
    [ -f "$target" ] || return 0
    temp=$target.interviewcraft-uninstall-$$.tmp
    awk -v begin="$path_begin" -v end="$path_end" '
        $0 == begin { inside=1; next }
        $0 == end { inside=0; next }
        !inside { print }
    ' "$target" > "$temp"
    mv "$temp" "$target"
}

old_ifs=$IFS; IFS='|'; for path_file in $path_files; do strip_block "$path_file"; done; IFS=$old_ifs
rm -f "$binary_path"
rmdir "$install_dir" 2>/dev/null || true
rm -f "$receipt_path"
printf 'InterviewCraft %s was uninstalled.\n' "$version"
printf 'Configuration, credentials, and %s/.interviewcraft data were preserved.\n' "$HOME"
