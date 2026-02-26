param(
    [string]$TargetDir = "",
    [switch]$Force,
    [switch]$InitProjectAgent
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($TargetDir)) {
    $userHome = [Environment]::GetFolderPath("UserProfile")
    $TargetDir = Join-Path $userHome ".gopi"
}

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path

$templates = @(
    "config.yaml.example",
    "models.yaml.example",
    "tools.yaml.example",
    "prompt.md.example"
)

Write-Host "Initializing gopi config..."
Write-Host "Target directory: $TargetDir"

if (-not (Test-Path $TargetDir)) {
    New-Item -ItemType Directory -Path $TargetDir | Out-Null
}

foreach ($template in $templates) {
    $src = Join-Path $scriptDir $template
    if (-not (Test-Path $src)) {
        Write-Warning "Template not found: $src"
        continue
    }

    $destName = $template -replace "\.example$", ""
    $dest = Join-Path $TargetDir $destName

    if ((Test-Path $dest) -and (-not $Force)) {
        Write-Host "Skip existing: $dest"
        continue
    }

    Copy-Item -Path $src -Destination $dest -Force
    Write-Host "Generated: $dest"
}

if ($InitProjectAgent) {
    $projectAgent = Join-Path (Get-Location).Path "AGENT.md"
    $agentTemplate = Join-Path $scriptDir "AGENT.md.example"
    if ((-not (Test-Path $projectAgent)) -or $Force) {
        Copy-Item -Path $agentTemplate -Destination $projectAgent -Force
        Write-Host "Generated project AGENT.md: $projectAgent"
    } else {
        Write-Host "Skip existing project AGENT.md: $projectAgent"
    }
}

Write-Host "Done."
Write-Host "Next steps:"
Write-Host "  1) Edit $TargetDir\config.yaml"
Write-Host "  2) (Optional) Edit $TargetDir\models.yaml / tools.yaml / prompt.md"
Write-Host "  3) Place AGENT.md in your project root if needed"
