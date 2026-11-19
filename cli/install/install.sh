#!/bin/sh
# Installs a published stable CLI release from catcursor/esp32-iphone-agent.
#   curl -fsSL https://raw.githubusercontent.com/catcursor/esp32-iphone-agent/sense-usb-lan/cli/install/install.sh | sh
# Env: ESP32AGENT_VERSION=0.1.0 (default: latest), ESP32AGENT_BIN_DIR (default ~/.local/bin),
#      ESP32AGENT_VERSIONS_URL (optional custom versions.json),
#      ESP32AGENT_ARCHIVE=/path/esp32agent_<os>_<arch>.tar.gz to install a local build (checksums.txt next to it,
#      or ESP32AGENT_CHECKSUMS=/path), ESP32AGENT_FFMPEG=yes|no to answer the ffmpeg question without a prompt.
set -eu

REPO=catcursor/esp32-iphone-agent
BIN_DIR=${ESP32AGENT_BIN_DIR:-$HOME/.local/bin}

say() { printf '%s\n' "$*"; }
die() { printf 'esp32agent install: %s\n' "$*" >&2; exit 1; }

case $(uname -s) in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) die "unsupported OS $(uname -s) (on Windows use install.ps1)" ;;
esac
case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *) die "unsupported CPU $(uname -m)" ;;
esac
name=esp32agent_${os}_${arch}.tar.gz

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1
  else shasum -a 256 "$1" | cut -d' ' -f1; fi
}

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# Accept JSON in either compact or pretty-printed form. Pinned and local installs need no JSON tool.
read_json() {
  if command -v python3 >/dev/null 2>&1; then
    python3 -c '
import json, re, sys
op, path, version, platform = sys.argv[1:]
with open(path, encoding="utf-8") as f: doc = json.load(f)
if op == "tags":
    for release in doc:
        if not release["draft"] and not release["prerelease"]: print(release["tag_name"])
elif op == "latest":
    version = doc["latest"]
    stable = r"(?:0|[1-9][0-9]*)(?:\.(?:0|[1-9][0-9]*)){2}"
    if (not isinstance(version, str) or not re.fullmatch(stable, version)
            or not any(r["version"] == version for r in doc["versions"])):
        sys.exit("invalid latest CLI version in the GitHub manifest")
    print(version)
else:
    version = version or doc["latest"]
    for release in doc["versions"]:
        if release["version"] == version:
            asset = release["assets"][platform]
            print(asset["url"], asset["sha256"], sep="\n")
            break
    else: sys.exit("version missing from custom index")' "$@"
  elif command -v perl >/dev/null 2>&1 && perl -MJSON::PP -e 1 2>/dev/null; then
    perl -MJSON::PP -0777 -e '
      my ($op,$path,$version,$platform) = @ARGV;
      open my $f, "<", $path or die $!;
      my $doc = decode_json(<$f>);
      if ($op eq "tags") {
        for (@$doc) { print "$_->{tag_name}\n" unless $_->{draft} || $_->{prerelease} }
      } elsif ($op eq "latest") {
        my $v = $doc->{latest};
        die "invalid latest CLI version in the GitHub manifest\n"
          unless defined($v) && $v =~ /\A(?:0|[1-9][0-9]*)(?:\.(?:0|[1-9][0-9]*)){2}\z/ && grep { $_->{version} eq $v } @{$doc->{versions}};
        print "$v\n";
      } else {
        $version ||= $doc->{latest};
        my ($r) = grep { $_->{version} eq $version } @{$doc->{versions}};
        my $a = $r->{assets}{$platform};
        die "asset missing from custom index\n" unless $a->{url} && $a->{sha256};
        print "$a->{url}\n$a->{sha256}\n";
      }' "$@"
  else
    die "release discovery needs Python 3 or Perl JSON::PP; set ESP32AGENT_VERSION to install a known GitHub release"
  fi
}

if [ -n "${ESP32AGENT_ARCHIVE:-}" ]; then
  [ -f "$ESP32AGENT_ARCHIVE" ] || die "no file $ESP32AGENT_ARCHIVE"
  cp "$ESP32AGENT_ARCHIVE" "$tmp/$name"
  sums=${ESP32AGENT_CHECKSUMS:-$(dirname "$ESP32AGENT_ARCHIVE")/checksums.txt}
  [ -f "$sums" ] || die "no checksums file $sums"
  cp "$sums" "$tmp/checksums.txt"
  say "installing esp32agent from $ESP32AGENT_ARCHIVE"
else
  version=${ESP32AGENT_VERSION:-}
  if [ -n "${ESP32AGENT_VERSIONS_URL:-}" ]; then
    curl -fsSL -o "$tmp/index" "$ESP32AGENT_VERSIONS_URL"
    read_json asset "$tmp/index" "$version" "${os}_${arch}" >"$tmp/asset"
    aurl=$(sed -n '1p' "$tmp/asset")
    asha=$(sed -n '2p' "$tmp/asset")
    [ -n "$aurl" ] && [ -n "$asha" ] || die "no $os/$arch build in the custom index"
    curl -fsSL -o "$tmp/$name" "$aurl" || die "download failed: $aurl"
    printf '%s  %s\n' "$asha" "$name" >"$tmp/checksums.txt"
  else
    if [ -z "$version" ]; then
      page=1
      while :; do
        status=$(curl -sSL -w '%{http_code}' -D "$tmp/headers" -o "$tmp/page" "https://api.github.com/repos/$REPO/releases?per_page=100&page=$page")
        case $status in
          200) ;;
          403|429)
            curl -fsSL -o "$tmp/index" "https://github.com/$REPO/releases/latest/download/versions.json"
            version=$(read_json latest "$tmp/index" "" "")
            break ;;
          *) die "GitHub release lookup returned HTTP $status" ;;
        esac
        read_json tags "$tmp/page" "" "" >>"$tmp/tags"
        grep -iq '^link:.*rel="next"' "$tmp/headers" || break
        page=$((page + 1))
      done
      if [ -z "$version" ]; then
        version=$(sed -nE 's/^cli-v((0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*))$/\1/p' "$tmp/tags" |
          LC_ALL=C sort -t. -k1,1nr -k2,2nr -k3,3nr | sed -n '1p')
      fi
      [ -n "$version" ] || die "no published stable CLI release on github.com/$REPO"
    fi
    url=https://github.com/$REPO/releases/download/cli-v$version
    say "downloading esp32agent $version ($os/$arch)"
    curl -fsSL -o "$tmp/$name" "$url/$name" || die "download failed: $url/$name"
    curl -fsSL -o "$tmp/checksums.txt" "$url/checksums.txt" || die "download failed: $url/checksums.txt"
  fi
fi

want=$(awk -v n="$name" '{sub(/\r$/,"")} $2 == n || $2 == "*"n { print tolower($1) }' "$tmp/checksums.txt")
[ -n "$want" ] || die "$name is not listed in checksums.txt"
got=$(sha256 "$tmp/$name")
[ "$want" = "$got" ] || die "checksum mismatch for $name (expected $want, got $got)"

tar -xzf "$tmp/$name" -C "$tmp" esp32agent || die "bad archive $name"
mkdir -p "$BIN_DIR"
install -m 755 "$tmp/esp32agent" "$BIN_DIR/esp32agent"
say "installed $BIN_DIR/esp32agent ($("$BIN_DIR/esp32agent" version))"

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) say "warning: $BIN_DIR is not on your PATH; add it, e.g.: echo 'export PATH=\"$BIN_DIR:\$PATH\"' >> ~/.profile" ;;
esac

# ffmpeg decodes the mirrored screen (screenshots need it).
if ! command -v ffmpeg >/dev/null 2>&1; then
  installer=""
  if command -v brew >/dev/null 2>&1; then installer="brew install ffmpeg"
  elif command -v apt-get >/dev/null 2>&1; then installer="sudo apt-get install -y ffmpeg"
  elif command -v dnf >/dev/null 2>&1; then installer="sudo dnf install -y ffmpeg"
  fi
  answer=${ESP32AGENT_FFMPEG:-}
  if [ -n "$installer" ] && [ -z "$answer" ] && [ -r /dev/tty ]; then
    printf 'ffmpeg is needed for screenshots. Run "%s" now? [y/N] ' "$installer"
    read -r answer </dev/tty || answer=n
  fi
  case $answer in
    y | Y | yes) [ -n "$installer" ] && sh -c "$installer" ;;
    *) say "ffmpeg not found: install it for screenshots (${installer:-see https://ffmpeg.org/download.html})" ;;
  esac
fi

cat <<EOF

Connect the board to this computer and run esp32agent pair-usb PORT.
Replace PORT with the board's serial device. Then run esp32agent device info.
EOF
