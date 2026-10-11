# Install a released Windows client without a Go SDK. Compatible with PowerShell 5.1+.
[CmdletBinding()]
param(
    [string]$Server = '',
    [string]$Name = 'webda.cli',
    [string]$InstallDir = (Join-Path $env:LOCALAPPDATA 'WebdaCLI'),
    [string]$Version = 'latest',
    [switch]$InstallCompletion
)
$ErrorActionPreference = 'Stop'
if ($env:OS -ne 'Windows_NT') { throw 'This installer supports Windows only.' }
if ($Name -notmatch '^[a-zA-Z0-9_][a-zA-Z0-9._-]*$' -or $Name -eq 'update' -or
    $Name -match '\.$|^(CON|PRN|AUX|NUL|COM[1-9]|LPT[1-9])(\.|$)') {
    throw 'Name must be a valid Windows command name using letters, numbers, dots, underscores, or hyphens.'
}
if ($InstallCompletion -and !$Server) { throw 'InstallCompletion requires Server.' }
if ($Server) {
    $serverUri = $null
    if (![Uri]::TryCreate($Server, [UriKind]::Absolute, [ref]$serverUri) -or
        $serverUri.Scheme -notin @('http', 'https') -or !$serverUri.Host -or
        $serverUri.UserInfo -or $serverUri.Query -or $serverUri.Fragment -or $Server -match '\s|\\') {
        throw 'Server must be an http(s) URL with a host and no credentials, whitespace, query, or fragment.'
    }
}
if ($Version -ne 'latest') {
    if (!$Version.StartsWith('v')) { $Version = 'v' + $Version }
    if ($Version -notmatch '^v[a-zA-Z0-9][a-zA-Z0-9._-]*$') { throw 'Invalid release version.' }
}
# PROCESSOR_ARCHITEW6432 identifies native architecture in an emulated shell.
$nativeArchitecture = $env:PROCESSOR_ARCHITEW6432
if (!$nativeArchitecture) { $nativeArchitecture = $env:PROCESSOR_ARCHITECTURE }
switch ($nativeArchitecture) {
    'AMD64' { $architecture = 'amd64' }
    'ARM64' { $architecture = 'arm64' }
    default { throw "Unsupported architecture: $nativeArchitecture" }
}
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12
$repository = 'https://github.com/loopingz/webda.cli'
if ($Version -eq 'latest') {
    $release = Invoke-RestMethod -Uri 'https://api.github.com/repos/loopingz/webda.cli/releases/latest' -TimeoutSec 120
    $Version = $release.tag_name
    if ($Version -notmatch '^[a-zA-Z0-9][a-zA-Z0-9._-]*$') { throw 'Invalid latest release tag.' }
}
$asset = "webda-cli-windows-$architecture.exe"
$releaseBase = "$repository/releases/download/$Version"
$workDir = Join-Path ([IO.Path]::GetTempPath()) ('webda-cli-install-' + [Guid]::NewGuid().ToString('N'))
$stagedBinary = $null
$backupBinary = $null
try {
    New-Item -ItemType Directory -Path $workDir | Out-Null
    Write-Host "Installing $Version (windows/$architecture)..."
    $checksums = Join-Path $workDir 'checksums.txt'
    Invoke-WebRequest -UseBasicParsing -Uri "$releaseBase/checksums.txt" -OutFile $checksums -TimeoutSec 120
    $entries = @(Get-Content $checksums | Where-Object { $_ -match ('^([a-fA-F0-9]{64})\s+\*?' + [regex]::Escape($asset) + '$') })
    if ($entries.Count -ne 1) { throw "Release checksums do not contain a unique valid entry for $asset." }
    $expected = ($entries[0] -split '\s+')[0]
    $download = Join-Path $workDir $asset
    Invoke-WebRequest -UseBasicParsing -Uri "$releaseBase/$asset" -OutFile $download -TimeoutSec 120
    if ((Get-FileHash $download -Algorithm SHA256).Hash -ne $expected) {
        throw 'Checksum mismatch; the installed client was not changed.'
    }
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    $InstallDir = (Resolve-Path $InstallDir).Path
    $target = Join-Path $InstallDir "$Name.exe"
    if (Test-Path $target -PathType Container) { throw "$target is a directory." }
    $stagedBinary = Join-Path $InstallDir ('.install-' + [Guid]::NewGuid().ToString('N') + '.exe')
    Copy-Item $download $stagedBinary
    if (Test-Path $target) {
        # PowerShell converts a null string argument to an empty path on .NET.
        $backupBinary = Join-Path $InstallDir ('.backup-' + [Guid]::NewGuid().ToString('N') + '.exe')
        [IO.File]::Replace($stagedBinary, $target, $backupBinary)
    } else {
        [IO.File]::Move($stagedBinary, $target)
    }
    $stagedBinary = $null
    Write-Host "Installed $target"
    if ($Server) {
        $setupArgs = @('setup', '--name', $Name, '--url', $Server)
        if ($InstallCompletion) { $setupArgs += '--install-completion' }
        & $target @setupArgs
        if ($LASTEXITCODE -ne 0) { throw "Binary installed, but server setup failed; run & '$target' setup to finish." }
    }
    if (($env:Path -split ';') -notcontains $InstallDir) { $env:Path = "$InstallDir;$env:Path" }
    Write-Host "Added $InstallDir to PATH for this session. For future terminals, add it to your user Path in Environment Variables."
    if ($Server) { Write-Host "Run $Name explore to sign in and browse operations." }
    else { Write-Host "Run $Name setup to configure a server." }
} finally {
    if ($stagedBinary -and (Test-Path $stagedBinary)) { Remove-Item -Force $stagedBinary }
    if ($backupBinary -and (Test-Path $backupBinary)) { Remove-Item -Force $backupBinary }
    if (Test-Path $workDir) { Remove-Item -Recurse -Force $workDir }
}
