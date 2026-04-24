param(
    [string]$InstallDir = "",
    [switch]$NoPath,
    [switch]$NoMenu,
    [switch]$NoStart
)

$ErrorActionPreference = "Stop"

$Repo = "sky10ai/sky10"
$UserAgent = "sky10-windows-installer"

if (-not $IsWindows -and $PSVersionTable.PSEdition -eq "Core") {
    throw "install.ps1 is only supported on Windows."
}

if ([string]::IsNullOrWhiteSpace($InstallDir)) {
    $InstallDir = Join-Path $env:LOCALAPPDATA "sky10\bin"
}

function Get-Sky10Arch {
    $arch = $env:PROCESSOR_ARCHITEW6432
    if ([string]::IsNullOrWhiteSpace($arch)) {
        $arch = $env:PROCESSOR_ARCHITECTURE
    }
    switch ($arch) {
        "AMD64" { return "amd64" }
        "ARM64" { return "arm64" }
        default { throw "Unsupported Windows architecture: $arch" }
    }
}

function Invoke-GitHubJson {
    param([string]$Uri)
    Invoke-RestMethod -Uri $Uri -Headers @{ "User-Agent" = $UserAgent }
}

function Get-ReleaseAsset {
    param(
        [object]$Release,
        [string]$Name,
        [switch]$Optional
    )

    $asset = $Release.assets | Where-Object { $_.name -eq $Name } | Select-Object -First 1
    if (-not $asset -and -not $Optional) {
        throw "Release $($Release.tag_name) does not include $Name."
    }
    return $asset
}

function Save-Asset {
    param(
        [object]$Asset,
        [string]$Destination
    )

    $tmp = Join-Path ([System.IO.Path]::GetTempPath()) ([System.IO.Path]::GetRandomFileName())
    try {
        Write-Host "Downloading $($Asset.name)..."
        Invoke-WebRequest -Uri $Asset.browser_download_url -Headers @{ "User-Agent" = $UserAgent } -OutFile $tmp
        Move-Item -Force $tmp $Destination
    } finally {
        if (Test-Path $tmp) {
            Remove-Item -Force $tmp
        }
    }
}

function Assert-Checksum {
    param(
        [object]$Release,
        [string]$ChecksumsAssetName,
        [string]$InstalledAssetName,
        [string]$Path
    )

    $checksumsAsset = Get-ReleaseAsset -Release $Release -Name $ChecksumsAssetName -Optional
    if (-not $checksumsAsset) {
        Write-Warning "$ChecksumsAssetName not found; skipping checksum verification for $InstalledAssetName."
        return
    }

    $manifest = Invoke-WebRequest -Uri $checksumsAsset.browser_download_url -Headers @{ "User-Agent" = $UserAgent }
    $line = ($manifest.Content -split "`n") |
        ForEach-Object { $_.Trim() } |
        Where-Object { $_ -match "^\S+\s+$([regex]::Escape($InstalledAssetName))$" } |
        Select-Object -First 1

    if (-not $line) {
        throw "$InstalledAssetName not found in $ChecksumsAssetName."
    }

    $expected = ($line -split "\s+")[0].ToLowerInvariant()
    $actual = (Get-FileHash -Algorithm SHA256 $Path).Hash.ToLowerInvariant()
    if ($actual -ne $expected) {
        throw "Checksum mismatch for $InstalledAssetName. Expected $expected, got $actual."
    }
}

function Add-UserPath {
    param([string]$Path)

    $current = [Environment]::GetEnvironmentVariable("Path", "User")
    $parts = @()
    if (-not [string]::IsNullOrWhiteSpace($current)) {
        $parts = $current -split ";"
    }

    if ($parts | Where-Object { $_ -ieq $Path }) {
        return
    }

    $newPath = if ([string]::IsNullOrWhiteSpace($current)) { $Path } else { "$Path;$current" }
    [Environment]::SetEnvironmentVariable("Path", $newPath, "User")
    $env:Path = "$Path;$env:Path"
    Write-Host "Added $Path to your user PATH. Open a new terminal to use it everywhere."
}

function Set-RunKey {
    param(
        [string]$Name,
        [string]$Command
    )

    $runKey = "HKCU:\Software\Microsoft\Windows\CurrentVersion\Run"
    New-Item -Path $runKey -Force | Out-Null
    New-ItemProperty -Path $runKey -Name $Name -Value $Command -PropertyType String -Force | Out-Null
}

function New-MenuShortcut {
    param([string]$MenuPath)

    $programs = Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs"
    New-Item -Path $programs -ItemType Directory -Force | Out-Null
    $shortcutPath = Join-Path $programs "sky10 Menu.lnk"

    $shell = New-Object -ComObject WScript.Shell
    $shortcut = $shell.CreateShortcut($shortcutPath)
    $shortcut.TargetPath = $MenuPath
    $shortcut.WorkingDirectory = Split-Path -Parent $MenuPath
    $shortcut.Save()
    Write-Host "Created Start Menu shortcut: $shortcutPath"
}

function Start-Sky10Process {
    param(
        [string]$Path,
        [string[]]$Arguments = @()
    )

    if ($Arguments.Count -gt 0) {
        Start-Process -FilePath $Path -ArgumentList $Arguments -WindowStyle Hidden | Out-Null
    } else {
        Start-Process -FilePath $Path -WindowStyle Hidden | Out-Null
    }
}

$arch = Get-Sky10Arch
$cliAssetName = "sky10-windows-$arch.exe"
$menuAssetName = "sky10-menu-windows-$arch.exe"

Write-Host "Installing sky10 for Windows/$arch..."
Write-Host "Install directory: $InstallDir"

$release = Invoke-GitHubJson "https://api.github.com/repos/$Repo/releases/latest"
Write-Host "Latest release: $($release.tag_name)"

New-Item -Path $InstallDir -ItemType Directory -Force | Out-Null

$cliPath = Join-Path $InstallDir "sky10.exe"
$cliAsset = Get-ReleaseAsset -Release $release -Name $cliAssetName
Stop-Process -Name "sky10" -ErrorAction SilentlyContinue
Save-Asset -Asset $cliAsset -Destination $cliPath
Assert-Checksum -Release $release -ChecksumsAssetName "checksums.txt" -InstalledAssetName $cliAssetName -Path $cliPath
Write-Host "Installed sky10.exe to $cliPath"

if (-not $NoMenu) {
    $menuPath = Join-Path $InstallDir "sky10-menu.exe"
    $menuAsset = Get-ReleaseAsset -Release $release -Name $menuAssetName -Optional
    if ($menuAsset) {
        Stop-Process -Name "sky10-menu" -ErrorAction SilentlyContinue
        Save-Asset -Asset $menuAsset -Destination $menuPath
        Assert-Checksum -Release $release -ChecksumsAssetName "checksums-menu.txt" -InstalledAssetName $menuAssetName -Path $menuPath
        Write-Host "Installed sky10-menu.exe to $menuPath"
        New-MenuShortcut -MenuPath $menuPath
        Set-RunKey -Name "sky10-menu" -Command "`"$menuPath`""
    } else {
        Write-Warning "$menuAssetName is not available in $($release.tag_name); skipping sky10-menu."
    }
}

if (-not $NoPath) {
    Add-UserPath -Path $InstallDir
}

Set-RunKey -Name "sky10-daemon" -Command "`"$cliPath`" serve"

if (-not $NoStart) {
    Write-Host "Starting sky10 daemon..."
    Start-Sky10Process -Path $cliPath -Arguments @("serve")

    if (-not $NoMenu) {
        $menuPath = Join-Path $InstallDir "sky10-menu.exe"
        if (Test-Path $menuPath) {
            Write-Host "Starting sky10-menu..."
            Start-Sky10Process -Path $menuPath
        }
    }
}

Write-Host ""
Write-Host "sky10 $($release.tag_name) installed."
Write-Host "Useful commands:"
Write-Host "  sky10 daemon status"
Write-Host "  sky10 ui open"
Write-Host "  sky10 invite"
Write-Host "  sky10 join <code>"
