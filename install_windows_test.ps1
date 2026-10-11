param([Parameter(Mandatory = $true)][string]$Binary)
$ErrorActionPreference = 'Stop'
$installer = [scriptblock]::Create((Get-Content "$PSScriptRoot/install.ps1" -Raw))
$root = Join-Path ([IO.Path]::GetTempPath()) ('webda-installer-test-' + [Guid]::NewGuid().ToString('N'))
$script:binaryFixture = (Resolve-Path $Binary).Path
$script:fixtureHash = (Get-FileHash $script:binaryFixture -Algorithm SHA256).Hash
$script:badHash = $false
$script:requests = @()
function Invoke-RestMethod {
    param($Uri, $TimeoutSec)
    $script:requests += $Uri
    return @{ tag_name = 'v9.9.9' }
}
function Invoke-WebRequest {
    param($Uri, $OutFile, $TimeoutSec, [switch]$UseBasicParsing)
    $script:requests += $Uri
    if ($Uri.EndsWith('/checksums.txt')) {
        $hash = $script:fixtureHash
        if ($script:badHash) { $hash = '0' * 64 }
        @("$hash  webda-cli-windows-amd64.exe", "$hash  webda-cli-windows-arm64.exe") | Set-Content $OutFile
    } else { Copy-Item $script:binaryFixture $OutFile }
}
function Assert($Condition, $Message) { if (!$Condition) { throw $Message } }
try {
    New-Item -ItemType Directory $root | Out-Null
    $env:USERPROFILE = Join-Path $root 'home'
    $env:HOME = $env:USERPROFILE
    New-Item -ItemType Directory $env:USERPROFILE | Out-Null
    $env:PROCESSOR_ARCHITEW6432 = ''
    foreach ($architecture in @('AMD64', 'ARM64')) {
        $env:PROCESSOR_ARCHITECTURE = $architecture
        $destination = Join-Path $root "$architecture with spaces and ' quote"
        & $installer -Server https://app.example.com/api -Name myapp -InstallDir $destination
        Assert (Test-Path "$destination/myapp.exe") 'Binary missing'
        $config = Get-Content "$env:USERPROFILE/.webdacli/config.yaml" -Raw
        Assert ($config.Contains('myapp: https://app.example.com/api')) 'Server setup failed'
        Assert (($env:Path -split ';') -contains $destination) 'Session PATH not updated'
        Assert ($script:requests -contains "https://github.com/loopingz/webda.cli/releases/download/v9.9.9/webda-cli-windows-$($architecture.ToLower()).exe") 'Wrong release or architecture'
    }
    $destination = Join-Path $root 'pinned'
    $script:requests = @()
    & $installer -Version 1.2.3 -InstallDir $destination
    Assert ($script:requests -contains 'https://github.com/loopingz/webda.cli/releases/download/v1.2.3/webda-cli-windows-arm64.exe') 'Version pin failed'
    Assert (!($script:requests -contains 'https://api.github.com/repos/loopingz/webda.cli/releases/latest')) 'Pinned install resolved latest'
    # Exercise replacement of an existing installation too.
    & $installer -Version 1.2.3 -InstallDir $destination
    $script:badHash = $true
    $failed = $false
    try { & $installer -InstallDir $destination } catch { $failed = $_.Exception.Message.Contains('Checksum mismatch') }
    Assert $failed 'Bad checksum accepted'
    Assert ((Get-FileHash "$destination/webda.cli.exe").Hash -eq $script:fixtureHash) 'Existing binary changed on failed checksum'
    $script:requests = @()
    foreach ($arguments in @(@{Name='../bad'}, @{Name='CON'}, @{Server='https://user:secret@example.com'}, @{Version='../bad'}, @{InstallCompletion=$true})) {
        $failed = $false
        try { & $installer @arguments } catch { $failed = $true }
        Assert $failed 'Invalid option accepted'
    }
    Assert ($script:requests.Count -eq 0) 'Invalid input triggered a download'
    Write-Host 'Windows installer integration tests passed.'
} finally { Remove-Item -Recurse -Force $root }
