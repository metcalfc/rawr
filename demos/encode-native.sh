#!/bin/bash
# Encode supplied GUI screenshot keyframes; this script never captures a screen.
set -euo pipefail

fail() { printf 'encode-native: %s\n' "$*" >&2; exit 1; }
if [[ $# -lt 1 || $# -gt 2 ]]; then
    printf 'Usage: %s FRAMES.ffconcat [OUTPUT.gif]\n' "$0" >&2
    exit 1
fi
for dependency in ffmpeg ffprobe awk sed; do
    command -v "$dependency" >/dev/null 2>&1 || fail "missing dependency: $dependency"
done

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
manifest_dir=$(CDPATH= cd -- "$(dirname -- "$1")" && pwd -P)
manifest="$manifest_dir/$(basename -- "$1")"
[[ -f "$manifest" && ! -L "$manifest" ]] || fail 'manifest must be a regular, non-symlink file'
output=${2:-"$repo_dir/assets/demo.gif"}
[[ "$output" == *.gif ]] || fail 'output must end in .gif'
output_dir=$(CDPATH= cd -- "$(dirname -- "$output")" && pwd -P)
output="$output_dir/$(basename -- "$output")"

# Accept a deliberately small ffconcat subset. Paths are relative PNG files
# below the manifest directory, without spaces, quotes, URLs or parent traversal.
# Example: file 'frames/01-shell.png' followed by duration 2.5.
file_pattern="^file '([A-Za-z0-9_./-]+[.]png)'$"
duration_pattern='^duration ([0-9]+([.][0-9]+)?)$'
frames=()
durations=()
line_number=0
while IFS= read -r line || [[ -n "$line" ]]; do
    line_number=$((line_number + 1))
    line=${line%$'\r'}
    if [[ $line_number -eq 1 ]]; then
        [[ "$line" == 'ffconcat version 1.0' ]] || fail 'first line must be ffconcat version 1.0'
        continue
    fi
    [[ -z "$line" || "$line" == \#* ]] && continue
    if [[ "$line" =~ $file_pattern ]]; then
        [[ ${#frames[@]} -eq ${#durations[@]} ]] || fail "missing duration before line $line_number"
        frames+=("${BASH_REMATCH[1]}")
    elif [[ "$line" =~ $duration_pattern ]]; then
        [[ ${#frames[@]} -eq $((${#durations[@]} + 1)) ]] || fail "unexpected duration at line $line_number"
        duration=${BASH_REMATCH[1]}
        awk -v n="$duration" 'BEGIN { exit !(n > 0 && n <= 60) }' || fail 'each duration must be greater than zero and at most 60 seconds'
        durations+=("$duration")
    else
        fail "unsupported manifest directive at line $line_number"
    fi
done < "$manifest"
[[ ${#durations[@]} -gt 0 && ${#durations[@]} -le 200 ]] || fail 'provide between 1 and 200 timed keyframes'
# ffconcat commonly repeats its last file so the last duration is honored. The
# encoder supplies that repetition if absent and accepts an existing one.
if [[ ${#frames[@]} -eq $((${#durations[@]} + 1)) ]]; then
    last=$((${#frames[@]} - 1))
    [[ "${frames[$last]}" == "${frames[$((last - 1))]}" ]] || fail 'untimed final file must repeat the last timed keyframe'
    unset 'frames[last]'
fi
[[ ${#frames[@]} -eq ${#durations[@]} ]] || fail 'every keyframe needs a duration'

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/rawr-native-encode.XXXXXX")
encoded=''
trap 'rm -rf "$work_dir"; if [[ -n "$encoded" ]]; then rm -f "$encoded"; fi' EXIT
trap 'exit 1' HUP INT TERM
concat="$work_dir/frames.ffconcat"
printf 'ffconcat version 1.0\n' > "$concat"
dimensions=''
last_path=''
for ((index = 0; index < ${#frames[@]}; index++)); do
    frame=${frames[$index]}
    [[ "$frame" != /* ]] || fail 'frame paths must be relative to the manifest'
    IFS=/ read -r -a components <<< "$frame"
    frame_path="$manifest_dir"
    for component in "${components[@]}"; do
        [[ -n "$component" && "$component" != . && "$component" != .. ]] || fail "invalid frame path: $frame"
        frame_path="$frame_path/$component"
        [[ ! -L "$frame_path" ]] || fail "symlink frame paths are not accepted: $frame"
    done
    [[ -f "$frame_path" ]] || fail "missing regular PNG frame: $frame"
    probe=$(ffprobe -v error -select_streams v:0 -show_entries stream=codec_name,width,height -of csv=p=0 "$frame_path") || fail "cannot inspect frame: $frame"
    [[ "$probe" =~ ^png,([0-9]+),([0-9]+)$ ]] || fail "frame must be a still PNG: $frame"
    [[ ${BASH_REMATCH[1]} -gt 0 && ${BASH_REMATCH[2]} -gt 0 ]] || fail "invalid frame dimensions: $frame"
    if [[ -z "$dimensions" ]]; then dimensions="$probe"; fi
    [[ "$probe" == "$dimensions" ]] || fail "frame dimensions differ: $frame"
    # ffconcat uses its own quoting, not shell parsing. Escape any quote in the
    # directory selected by the caller; frame names themselves exclude quotes.
    last_path=$(printf '%s' "$frame_path" | sed "s/'/'\\\\''/g")
    printf "file '%s'\nduration %s\n" "$last_path" "${durations[$index]}" >> "$concat"
done
printf "file '%s'\n" "$last_path" >> "$concat"

encoded=$(mktemp "$output_dir/.rawr-native.XXXXXX")
ffmpeg -nostdin -hide_banner -loglevel warning -y \
    -f concat -safe 0 -protocol_whitelist file -i "$concat" \
    -filter_complex 'scale=1416:-2:flags=lanczos,split[image][colors];[colors]palettegen=stats_mode=diff[palette];[image][palette]paletteuse=dither=bayer:bayer_scale=3:diff_mode=rectangle' \
    -fps_mode vfr -loop 0 -f gif "$encoded"
mv -f -- "$encoded" "$output"
encoded=''
printf 'Encoded %s supplied screenshot keyframes to %s (1416px wide; timed stills, not continuous video).\n' "${#frames[@]}" "$output"
