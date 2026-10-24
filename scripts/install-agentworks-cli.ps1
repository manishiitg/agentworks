# install-agentworks.ps1 - install the AgentWorks CLI from your server on Windows and (optionally) sign in.
#
# Served at https://<server>/api/downloads/cli/install-agentworks.ps1 and shown prefilled in Crew settings > Laptop access.
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
if (-not $Server) { $Server = 'https://network.agentworkshq.com' }
$Server = $Server.TrimEnd('/')

$machineArchitecture = $env:PROCESSOR_ARCHITEW6432
if (-not $machineArchitecture) { $machineArchitecture = $env:PROCESSOR_ARCHITECTURE }
switch ($machineArchitecture) {
  'AMD64' { $arch = 'amd64' }
  'ARM64' { $arch = 'arm64' }
  default { throw "unsupported architecture: $machineArchitecture (need AMD64 or ARM64)" }
}
$asset = "agentworks-windows-$arch.exe"
$base = "$Server/api/downloads/cli"
$stage = Join-Path ([IO.Path]::GetTempPath()) ("agentworks-" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $stage | Out-Null
try {
  Write-Host "Downloading $asset ..."
  Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset.gz" -OutFile (Join-Path $stage "$asset.gz")
  $compressed = [IO.File]::OpenRead((Join-Path $stage "$asset.gz"))
  try {
    $gzip = [IO.Compression.GZipStream]::new($compressed, [IO.Compression.CompressionMode]::Decompress)
    try {
      $unpacked = [IO.File]::Create((Join-Path $stage $asset))
      try { $gzip.CopyTo($unpacked) } finally { $unpacked.Dispose() }
    } finally { $gzip.Dispose() }
  } finally { $compressed.Dispose() }
  Invoke-WebRequest -UseBasicParsing -Uri "$base/$asset.sha256" -OutFile (Join-Path $stage "$asset.sha256")

  Write-Host 'Verifying checksum ...'
  $expected = ((Get-Content (Join-Path $stage "$asset.sha256") -Raw).Trim() -split '\s+')[0].ToLower()
  $actual = (Get-FileHash -Algorithm SHA256 (Join-Path $stage $asset)).Hash.ToLower()
  if ($expected -ne $actual) { throw "checksum mismatch for $asset; refusing to install" }

  # The CLI's version command is local-only. Invoke the staged file with the
  # call operator so spaces and apostrophes in the path stay one argument.
  $candidate = Join-Path $stage $asset
  $versionOutput = & $candidate version
  if ($LASTEXITCODE -ne 0) { throw 'The downloaded CLI could not run; the installed copy was kept.' }
  $candidateVersion = $versionOutput | ConvertFrom-Json
  if ($candidateVersion.os -ne 'windows' -or $candidateVersion.arch -ne $arch -or -not $candidateVersion.version) {
    throw 'The downloaded CLI reported the wrong platform; the installed copy was kept.'
  }
  New-Item -ItemType Directory -Force -Path $Dir | Out-Null
  $target = Join-Path $Dir 'agentworks.exe'
  $oldTarget = "$target.old-" + [guid]::NewGuid().ToString('N')
  if (Test-Path $target) { Move-Item $target $oldTarget } # a running copy can be renamed, not replaced
  Move-Item -Force $candidate $target
  Remove-Item $oldTarget -Force -ErrorAction SilentlyContinue
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
Write-Host 'Done. Open your Crew settings in AgentWorks and choose Laptop access to connect this computer.'
