# Installs a published stable Windows CLI release from catcursor/esp32-iphone-agent.
#   irm https://raw.githubusercontent.com/catcursor/esp32-iphone-agent/sense-usb-lan/cli/install/install.ps1 | iex
# Env: ESP32AGENT_VERSION (default: latest), ESP32AGENT_BIN_DIR (default ~\.local\bin),
#      ESP32AGENT_VERSIONS_URL (optional custom versions.json),
#      ESP32AGENT_ARCHIVE=C:\path\esp32agent_windows_<arch>.zip for a local build (checksums.txt next to it, or
#      ESP32AGENT_CHECKSUMS), ESP32AGENT_FFMPEG=yes|no to answer the ffmpeg question without a prompt.
$ErrorActionPreference = 'Stop'
$repo = 'catcursor/esp32-iphone-agent'
$binDir = if ($env:ESP32AGENT_BIN_DIR) { $env:ESP32AGENT_BIN_DIR } else { Join-Path $HOME '.local\bin' }

$arch = switch ($env:PROCESSOR_ARCHITECTURE) { 'ARM64' { 'arm64' } 'AMD64' { 'amd64' } default { throw "unsupported CPU $env:PROCESSOR_ARCHITECTURE" } }
$name = "esp32agent_windows_$arch.zip"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("esp32agent-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    $zip = Join-Path $tmp $name
    $sums = Join-Path $tmp 'checksums.txt'
    if ($env:ESP32AGENT_ARCHIVE) {
        Copy-Item $env:ESP32AGENT_ARCHIVE $zip
        $src = if ($env:ESP32AGENT_CHECKSUMS) { $env:ESP32AGENT_CHECKSUMS } else { Join-Path (Split-Path $env:ESP32AGENT_ARCHIVE) 'checksums.txt' }
        Copy-Item $src $sums
        Write-Host "installing esp32agent from $env:ESP32AGENT_ARCHIVE"
    } else {
        $version = $env:ESP32AGENT_VERSION
        if ($env:ESP32AGENT_VERSIONS_URL) {
            $list = Invoke-RestMethod $env:ESP32AGENT_VERSIONS_URL
            if (-not $version) { $version = $list.latest }
            $rel = $list.versions | Where-Object { $_.version -eq $version } | Select-Object -First 1
            $asset = $rel.assets."windows_$arch"
            if (-not $asset.url -or $asset.sha256 -notmatch '^[0-9a-f]{64}$') {
                throw "no checked build for windows/$arch version $version in the custom index"
            }
            Write-Host "downloading esp32agent $version (windows/$arch)"
            Invoke-WebRequest $asset.url -OutFile $zip -UseBasicParsing
            "$($asset.sha256)  $name" | Set-Content -LiteralPath $sums -Encoding ascii
        } else {
            if (-not $version) {
                $page = 1
                $latest = $null
                do {
                    $releases = Invoke-RestMethod "https://api.github.com/repos/$repo/releases?per_page=100&page=$page"
                    foreach ($release in $releases) {
                        if (-not $release.draft -and -not $release.prerelease -and
                            $release.tag_name -cmatch '^cli-v((?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*))$') {
                            $candidate = [Version]$Matches[1]
                            if ($null -eq $latest -or $candidate -gt $latest) { $latest = $candidate }
                        }
                    }
                    $page++
                } while ($releases.Count -eq 100)
                if ($null -eq $latest) { throw "no published stable CLI release on github.com/$repo" }
                $version = $latest.ToString()
            }
            $url = "https://github.com/$repo/releases/download/cli-v$version"
            Write-Host "downloading esp32agent $version (windows/$arch)"
            Invoke-WebRequest "$url/$name" -OutFile $zip -UseBasicParsing
            Invoke-WebRequest "$url/checksums.txt" -OutFile $sums -UseBasicParsing
        }
    }

    $line = Get-Content $sums | Where-Object { ($_ -split '\s+')[1] -in @($name, "*$name") } | Select-Object -First 1
    if (-not $line) { throw "$name is not listed in checksums.txt" }
    $want = ($line -split '\s+')[0].ToLower()
    $got = (Get-FileHash $zip -Algorithm SHA256).Hash.ToLower()
    if ($want -ne $got) { throw "checksum mismatch for $name (expected $want, got $got)" }

    Expand-Archive $zip -DestinationPath $tmp -Force
    New-Item -ItemType Directory -Force -Path $binDir | Out-Null
    $dest = Join-Path $binDir 'esp32agent.exe'
    Copy-Item (Join-Path $tmp 'esp32agent.exe') $dest -Force
    Write-Host "installed $dest ($(& $dest version))"
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (-not (($env:Path + ';' + $userPath) -split ';' | Where-Object { $_ -eq $binDir })) {
    Write-Warning "$binDir is not on your PATH. Add it: [Environment]::SetEnvironmentVariable('Path', `"$binDir;`" + [Environment]::GetEnvironmentVariable('Path','User'), 'User')"
}

# ffmpeg decodes the mirrored screen (screenshots need it).
if (-not (Get-Command ffmpeg -ErrorAction SilentlyContinue)) {
    $answer = $env:ESP32AGENT_FFMPEG
    $winget = Get-Command winget -ErrorAction SilentlyContinue
    if ($winget -and -not $answer) { $answer = Read-Host 'ffmpeg is needed for screenshots. Install it with winget now? [y/N]' }
    if ($winget -and $answer -match '^(y|yes)$') { winget install --id Gyan.FFmpeg -e --accept-source-agreements --accept-package-agreements }
    else { Write-Host 'ffmpeg not found: install it for screenshots (winget install Gyan.FFmpeg)' }
}

Write-Host @'

Connect the board to this computer and run esp32agent pair-usb PORT.
Replace PORT with the board's serial device. Then run esp32agent device info.
'@
