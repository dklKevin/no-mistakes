$ErrorActionPreference = "Stop"

$repo = "kunchenguid/no-mistakes"
$installDir = "$env:LOCALAPPDATA\no-mistakes"
$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }

if ($env:NO_MISTAKES_VERSION) {
    $version = $env:NO_MISTAKES_VERSION
} else {
    $release = Invoke-RestMethod -Uri "https://api.github.com/repos/$repo/releases/latest"
    $version = $release.tag_name
}
if (-not $version) {
    throw "Could not determine latest release"
}
if ($version -notmatch '^[A-Za-z0-9._-]+$') {
    throw "Invalid release version: $version"
}

$filename = "no-mistakes-$version-windows-$arch.zip"
$assetBase = "https://github.com/$repo/releases/download/$version"
$url = "$assetBase/$filename"
$checksumsUrl = "$assetBase/checksums.txt"

$tmpDir = New-TemporaryFile | ForEach-Object {
    Remove-Item $_
    New-Item -ItemType Directory -Path $_
}

Write-Host "Downloading no-mistakes $version for windows/$arch..."
Invoke-WebRequest -Uri $url -OutFile "$tmpDir\$filename"
Invoke-WebRequest -Uri $checksumsUrl -OutFile "$tmpDir\checksums.txt"

$checksumsPath = Join-Path $tmpDir "checksums.txt"
if (-not (Test-Path $checksumsPath) -or (Get-Item $checksumsPath).Length -le 0) {
    throw "checksums.txt is missing or empty"
}

$expected = $null
Get-Content $checksumsPath | ForEach-Object {
    $parts = @($_ -split '\s+')
    if ($parts.Count -ge 2 -and $parts[1] -eq $filename) {
        $expected = $parts[0].ToLowerInvariant()
    }
}
if (-not $expected) {
    throw "checksums.txt has no entry for $filename"
}

$actual = (Get-FileHash -Path (Join-Path $tmpDir $filename) -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actual -ne $expected) {
    throw "checksum mismatch for ${filename}: got $actual want $expected"
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
