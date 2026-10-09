# install-agentworks.ps1 - install the AgentWorks CLI from your server on Windows and (optionally) sign in.
#
# Served at https://<server>/api/downloads/cli/install-agentworks.ps1 and shown prefilled in Code > settings > Location.
#
#   & ([scriptblock]::Create((irm 'https://<server>/api/downloads/cli/install-agentworks.ps1'))) -Server 'https://<server>' -NoLogin
#
# Installs %LOCALAPPDATA%\agentworks\agentworks.exe, adds that folder to your user PATH, and checks for Git for Windows (commands
# the agent runs on your computer go through Git Bash). Nothing needs administrator rights.
param(
  [string]$Server = $env:AGENTWORKS_SERVER,
  [string]$Dir = (Join-Path $env:LOCALAPPDATA 'agentworks'),
  [switch]$NoLogin
)
$ErrorActionPreference = 'Stop'
if (-not $Server) { throw 'missing -Server (or the AGENTWORKS_SERVER environment variable)' }
$Server = $Server.TrimEnd('/')

switch ($env:PROCESSOR_ARCHITECTURE) {
  'AMD64' { $arch = 'amd64' }
  'ARM64' { $arch = 'arm64' }
  default { throw "unsupported architecture: $($env:PROCESSOR_ARCHITECTURE) (need AMD64 or ARM64)" }
}
$asset = "agentworks-windows-$arch.exe"
$base = "$Server/api/downloads/cli"
$stage = Join-Path ([IO.Path]::GetTempPath()) ("agentworks-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $stage | Out-Null
try {
  Write-Host "Downloading $asset ..."
  Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset" -OutFile (Join-Path $stage $asset)
  Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset.sha256" -OutFile (Join-Path $stage "$asset.sha256")

  Write-Host 'Verifying checksum ...'
  $expected = ((Get-Content (Join-Path $stage "$asset.sha256") -Raw).Trim() -split '\s+')[0].ToLower()
  $actual = (Get-FileHash -Algorithm SHA256 (Join-Path $stage $asset)).Hash.ToLower()
  if ($expected -ne $actual) { throw "checksum mismatch for $asset; refusing to install" }

  New-Item -ItemType Directory -Force -Path $Dir | Out-Null
  $target = Join-Path $Dir 'agentworks.exe'
  if (Test-Path $target) { Move-Item -Force $target "$target.old" -ErrorAction SilentlyContinue } # a running copy can be renamed, not replaced
  Move-Item -Force (Join-Path $stage $asset) $target
  Remove-Item "$target.old" -Force -ErrorAction SilentlyContinue
  Write-Host "Installed $target"
} finally {
  Remove-Item -Recurse -Force $stage -ErrorAction SilentlyContinue
}

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if (($userPath -split ';') -notcontains $Dir) {
  [Environment]::SetEnvironmentVariable('Path', (($userPath, $Dir) -ne '' -join ';'), 'User')
  $env:Path = "$env:Path;$Dir"
  Write-Host "Added $Dir to your PATH (new terminals pick it up)."
}

$bash = @("$env:ProgramFiles\Git\bin\bash.exe", "$env:ProgramW6432\Git\bin\bash.exe", "${env:ProgramFiles(x86)}\Git\bin\bash.exe", "$env:LOCALAPPDATA\Programs\Git\bin\bash.exe") | Where-Object { $_ -and (Test-Path $_) } | Select-Object -First 1
if (-not $bash) {
  Write-Warning 'Git for Windows was not found. File tools work without it, but commands the agent runs on your computer need it: https://git-scm.com/download/win'
}

if (-not $NoLogin) {
  Write-Host "Signing in to $Server ..."
  & $target login --server $Server
}
Write-Host 'Done. In your project folder run: agentworks start --server <server> --workspace "<workspace name>"'
