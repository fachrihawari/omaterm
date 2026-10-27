#!/usr/bin/env bash
# Renders a promo video for posts about omaterm: one of TestSiteVideo's
# clips, with captions, and for the launch clip an end card.
#
#   scripts/site-video.sh <clip> <format> [out.mp4]
#
# clip is launch, status, cli, lastturn, resume, phone, allinone, splits,
# files, ports or settings.
# format is wide (1920×1080, captions over the app), tall (1080×1350, the
# 4:5 a phone's feed shows biggest, captions above the app) or site
# (1920×1200 for the website, no captions, with a poster picture of its
# last frame beside it). out defaults to ~/Videos/omaterm-<clip>-<format>.mp4,
# or site/assets/video/<clip>.mp4 for site.
#
# Needs ffmpeg (with libx264 and drawtext), ImageMagick and Noto Sans.
set -euo pipefail

clip=${1:?usage: scripts/site-video.sh <clip> <wide|tall|site> [out.mp4]}
format=${2:?usage: scripts/site-video.sh <clip> <wide|tall|site> [out.mp4]}
cd "$(dirname "$0")/.."
default=$HOME/Videos/omaterm-$clip-$format.mp4
[ "$format" != site ] || default=site/assets/video/$clip.mp4
out=$(realpath -m "${3:-$default}")
mkdir -p "$(dirname "$out")"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/frames"

bold=$(fc-match -f '%{file}' 'Noto Sans:bold')
regular=$(fc-match -f '%{file}' 'Noto Sans')
mono=$(fc-match -f '%{file}' 'JetBrains Mono')
bg='#0d1117'
fps=30

SITE_VIDEO=$clip SITE_VIDEO_FORMAT=$format SITE_VIDEO_DIR="$work/frames" \
	go test -run '^TestSiteVideo$' -count=1 .

# The website's: no captions, as the page says what it shows, and small
# enough to load as the page scrolls to it.
if [ "$format" = site ]; then
	ffmpeg -loglevel error -y -framerate $fps -i "$work/frames/%05d.png" \
		-c:v libx264 -preset veryslow -crf 28 -pix_fmt yuv420p -movflags +faststart "$out"
	last=$(ls "$work/frames" | grep -c '\.png$')
	magick "$work/frames/$(printf '%05d' $((last - 1))).png" -quality 82 "${out%.mp4}.jpg"
	echo "$out"
	exit
fi

# A wide video's captions sit in a pill over the panes' empty lower half;
# a tall one's in the band above the app, in two lines. Each fades in and
# out.
if [ "$format" = tall ]; then
	filters=("pad=1080:1350:0:250:color=$bg")
	size=1080x1350
else
	filters=()
	size=1920x1080
fi
i=0
while read -r start end text; do
	[ -n "$start" ] || continue
	a=$(awk "BEGIN{print $start+0.15}")
	b=$(awk "BEGIN{print $end-0.15}")
	alpha="if(lt(t\,$a+0.3)\,max(0\,(t-$a)/0.3)\,if(gt(t\,$b-0.3)\,max(0\,($b-t)/0.3)\,1))"
	if [ "$format" = tall ]; then
		printf '%s' "${text// | /$'\n'}" >"$work/caption$i.txt"
		place="fontsize=58:line_spacing=14:text_align=C:x=(w-text_w)/2:y=(250-text_h)/2+6"
	else
		printf '%s' "${text// | / }" >"$work/caption$i.txt"
		place="fontsize=54:box=1:boxcolor=0x0d1117@0.88:boxborderw=30|44:x=(w-text_w)/2:y=h-text_h-170"
	fi
	filters+=("drawtext=fontfile=$bold:textfile=$work/caption$i.txt:fontcolor=white:$place:enable='between(t,$a,$b)':alpha='$alpha'")
	i=$((i + 1))
done <"$work/frames/captions.txt"
vf=$(IFS=,; echo "${filters[*]}")

encode=(-c:v libx264 -preset slow -crf 18 -pix_fmt yuv420p -movflags +faststart)
if [ "$clip" != launch ]; then
	ffmpeg -loglevel error -y -framerate $fps -i "$work/frames/%05d.png" -vf "$vf" "${encode[@]}" "$out"
	echo "$out"
	exit
fi
ffmpeg -loglevel error -y -framerate $fps -i "$work/frames/%05d.png" -vf "$vf" "${encode[@]}" "$work/scenes.mp4"

# The end card: the logo, the name, what it is and where to get it.
if [ "$format" = tall ]; then
	tagline=$'Terminal untuk ngejalanin\ncoding agent berdampingan'
	ys=(-330 -110 50 210 320)
else
	tagline='Terminal untuk ngejalanin coding agent berdampingan'
	ys=(-230 -20 90 160 270)
fi
magick -background none -density 600 site/assets/logo.svg -resize 220x220 "$work/logo.png"
magick -size $size xc:"$bg" \
	"$work/logo.png" -gravity center -geometry +0${ys[0]} -composite \
	-fill '#e6edf3' -font "$bold" -pointsize 112 -annotate +0${ys[1]} 'omaterm' \
	-fill '#9da7b3' -font "$regular" -pointsize 46 -interline-spacing 8 -annotate +0+${ys[2]} "$tagline" \
	-fill '#9da7b3' -font "$regular" -pointsize 38 -annotate +0+${ys[3]} 'Open source · MIT · Linux, macOS, Windows' \
	-fill '#2f81f7' -font "$mono" -pointsize 44 -annotate +0+${ys[4]} 'fachrihawari.github.io/omaterm' \
	"$work/end.png"

scene_len=$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$work/scenes.mp4")
offset=$(awk "BEGIN{print $scene_len-0.6}")
ffmpeg -loglevel error -y -i "$work/scenes.mp4" -loop 1 -framerate $fps -t 4.5 -i "$work/end.png" \
	-filter_complex "[1]format=yuv420p,settb=AVTB[e];[0]settb=AVTB[s];[s][e]xfade=transition=fade:duration=0.6:offset=$offset,format=yuv420p" \
	-r $fps "${encode[@]}" "$out"

echo "$out"
