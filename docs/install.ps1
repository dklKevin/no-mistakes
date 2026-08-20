$ErrorActionPreference = "Stop"

$repo = "kunchenguid/no-mistakes"
$installDir = "$env:LOCALAPPDATA\no-mistakes"
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }

# Pin the release tag. Scraping GitHub's moving "latest" pointer is unpinned
# and also skips prereleases, which is how 1.54/1.55 disappeared from the
# installer. Override with NO_MISTAKES_VERSION only for a specific tag or tests.
$version = if ($env:NO_MISTAKES_VERSION) { $env:NO_MISTAKES_VERSION } else { "v1.55.0" }
if ($version -notmatch '^v\d+\.\d+\.\d+' -or $version -match '[/\\]|\.\.') {
    throw "Invalid version: $version (expected vMAJOR.MINOR.PATCH)"
}

$filename = "no-mistakes-$version-windows-$arch.zip"
$url = "https://github.com/$repo/releases/download/$version/$filename"
$checksumsUrl = "https://github.com/$repo/releases/download/$version/checksums.txt"

$tmpDir = New-TemporaryFile | ForEach-Object {
    Remove-Item $_
    New-Item -ItemType Directory -Path $_
}

Write-Host "Downloading no-mistakes $version for windows/$arch..."
Invoke-WebRequest -Uri $url -OutFile "$tmpDir\$filename"

Write-Host "Verifying checksums.txt..."
$checksumsPath = Join-Path $tmpDir "checksums.txt"
try {
    Invoke-WebRequest -Uri $checksumsUrl -OutFile $checksumsPath
} catch {
    throw "Failed to download checksums.txt for $version"
}
if (-not (Test-Path $checksumsPath) -or (Get-Item $checksumsPath).Length -eq 0) {
    throw "checksums.txt for $version is empty"
}

$expected = $null
Get-Content $checksumsPath | ForEach-Object {
    $parts = $_ -split '\s+'
    if ($parts.Count -ge 2 -and ($parts[1] -eq $filename -or $parts[1] -eq "*$filename")) {
        $expected = $parts[0]
    }
}
if (-not $expected) {
    throw "checksums.txt has no SHA-256 for $filename"
}

$actual = (Get-FileHash -Algorithm SHA256 -Path (Join-Path $tmpDir $filename)).Hash
if ($actual.ToLowerInvariant() -ne $expected.ToLowerInvariant()) {
    throw "checksum mismatch for $filename : got $actual want $expected"
}

Expand-Archive -Path "$tmpDir\$filename" -DestinationPath $tmpDir -Force

New-Item -ItemType Directory -Path $installDir -Force | Out-Null
Move-Item -Path "$tmpDir\no-mistakes.exe" -Destination "$installDir\no-mistakes.exe" -Force
Remove-Item -Recurse -Force $tmpDir

$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$installDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$userPath;$installDir", "User")
    Write-Host "Added $installDir to user PATH. Restart your terminal."
}

$restart = Start-Process -FilePath "$installDir\no-mistakes.exe" -ArgumentList @(
    "daemon",
    "restart"
) -Wait -PassThru -NoNewWindow
if ($restart.ExitCode -ne 0) {
    throw "Failed to restart daemon (exit code $($restart.ExitCode))"
}

Write-Host "no-mistakes $version installed to $installDir\no-mistakes.exe"
