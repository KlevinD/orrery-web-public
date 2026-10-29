Describe 'dev.ps1 PostgreSQL startup' {
  $DevScript = Join-Path $PSScriptRoot 'dev.ps1'
  $DevScriptText = Get-Content -Raw -LiteralPath $DevScript

  It 'runs the context graph production gate before local dev tests can pass' {
    $GateScript = Join-Path $PSScriptRoot 'scripts\check-context-graph-production-gate.ps1'
    if (-not (Test-Path -LiteralPath $GateScript)) {
      throw "Expected context graph production gate script at $GateScript."
    }

    & $GateScript
    if ($LASTEXITCODE -ne 0) {
      throw "Context graph production gate failed with exit code $LASTEXITCODE."
    }
  }

  It 'does not wait inside pg_ctl start' {
    if ($DevScriptText -cmatch 'pg_ctl[^\r\n]*start[^\r\n]*\s-w\b') {
      throw 'dev.ps1 should not call pg_ctl start with -w.'
    }
    if ($DevScriptText -cmatch 'PgCtlPath\s+start[^\r\n]*\s-w\b') {
      throw 'dev.ps1 should not call $PgCtlPath start with -w.'
    }
    if ($DevScriptText -cmatch '&\s+\$PgCtlPath\s+start\b') {
      throw 'dev.ps1 should not run pg_ctl start in the current PowerShell process.'
    }
    if ($DevScriptText -cnotmatch 'Start-Process[\s\S]*-FilePath\s+\$PgCtlPath[\s\S]*start[\s\S]*-W') {
      throw 'dev.ps1 should launch pg_ctl start in a hidden background process.'
    }
    if ($DevScriptText -cnotmatch 'start'', ''-D'', \$DataRoot, ''-l'', \$LogPath, ''-W') {
      throw 'dev.ps1 should force pg_ctl start into no-wait mode with -W.'
    }
  }

  It 'uses script-side readiness polling before continuing' {
    if ($DevScriptText -notmatch 'function Wait-DevPostgresReady') {
      throw 'dev.ps1 should define Wait-DevPostgresReady.'
    }
    if ($DevScriptText -notmatch 'pg_isready') {
      throw 'dev.ps1 should use pg_isready for readiness checks.'
    }
    if ($DevScriptText -notmatch 'Start-Sleep -Milliseconds 500') {
      throw 'dev.ps1 should poll readiness with a short sleep.'
    }
  }

  It 'reuses an already listening PostgreSQL before calling pg_ctl status or start' {
    $FunctionMatch = [regex]::Match($DevScriptText, 'function Start-DevPostgresServer\([\s\S]*?\n}\r?\n\r?\nfunction Ensure-DevPostgresDatabases')
    if (-not $FunctionMatch.Success) {
      throw 'dev.ps1 should define Start-DevPostgresServer before Ensure-DevPostgresDatabases.'
    }

    $FunctionText = $FunctionMatch.Value
    $ReadyCheckIndex = $FunctionText.IndexOf('Test-DevPostgresListening')
    $StatusIndex = $FunctionText.IndexOf('PgCtlPath status')
    $StartIndex = $FunctionText.IndexOf("Start-Process")

    if ($ReadyCheckIndex -lt 0) {
      throw 'Start-DevPostgresServer should first check whether PostgreSQL is already listening on the configured host and port.'
    }
    if ($StatusIndex -lt 0 -or $StartIndex -lt 0) {
      throw 'Start-DevPostgresServer should still contain pg_ctl status/start fallback logic.'
    }
    if ($ReadyCheckIndex -gt $StatusIndex -or $ReadyCheckIndex -gt $StartIndex) {
      throw 'Start-DevPostgresServer should reuse a listening PostgreSQL before pg_ctl status/start.'
    }
  }

  It 'prepares one shared development PostgreSQL database by default' {
    if ($DevScriptText -notmatch "\$DefaultDevPostgresDatabase = 'orrery'") {
      throw 'dev.ps1 should default to one shared PostgreSQL database named orrery.'
    }
    if ($DevScriptText -match 'DevDatabaseRoles') {
      throw 'dev.ps1 should not prepare split role databases for local development.'
    }
    if ($DevScriptText -match 'ORRERY_DEV_POSTGRES_DATABASE_PREFIX') {
      throw 'dev.ps1 should use ORRERY_DEV_POSTGRES_DATABASE, not a split database prefix.'
    }
  }

  It 'ships the default development YAML with one physical database URL' {
    $DevConfig = Join-Path $PSScriptRoot 'config\orrery.development.yaml'
    $DevConfigText = Get-Content -Raw -LiteralPath $DevConfig
    if ($DevConfigText -notmatch 'password_env:\s*ORRERY_DATABASE_PASSWORD') {
      throw 'config/orrery.development.yaml should select the database password secret env.'
    }
    if ($DevConfigText -notmatch 'default_url:\s*"postgres://orrery@127\.0\.0\.1:5433/orrery\?sslmode=disable"') {
      throw 'config/orrery.development.yaml should keep passwords out of the shared database URL.'
    }
    foreach ($Role in @('core', 'audit', 'events', 'jobs', 'runtime', 'observability')) {
      if ($DevConfigText -notmatch "(?m)^\s+${Role}:\s*`"postgres://orrery@127\.0\.0\.1:5433/orrery\?sslmode=disable`"\r?$") {
        throw "config/orrery.development.yaml should map database role '$Role' to the shared development database."
      }
    }
  }

  It 'uses a persistent direct remote database env by default' {
    foreach ($Expected in @(
      '[switch]$Local',
      '$RemoteEnvFile',
      '$RemoteConfigTemplate',
      '$RemoteRuntimeConfig',
      'function Read-DotEnvFile',
      'function Assert-RemoteDatabaseEnvironment',
      'function Test-RemoteRuntimeStateMatches',
      'ORRERY_SKIP_DEFAULT_PROFILE_BOOTSTRAP',
      '-disable-background-workers'
    )) {
      if ($DevScriptText -notmatch [regex]::Escape($Expected)) {
        throw "dev.ps1 remote mode should contain '$Expected'."
      }
    }
    $StartMatch = [regex]::Match($DevScriptText, 'function Start-All[\s\S]*?\n}\r?\n\r?\nfunction Stop-All')
    if ($StartMatch.Value -notmatch '-not \(Test-RemoteDevConfigSelected \$ApiEnvironment\)') {
      throw 'Remote mode should bypass local database initialization.'
    }
    if ($StartMatch.Value -notmatch "\$ApiArguments \+= '-disable-background-workers'") {
      throw 'Remote mode should disable local queue and gateway workers.'
    }
    if ($StartMatch.Value -notmatch 'Stop-StaleRemoteModeApi \$RequestedApiPort') {
      throw 'Remote mode should replace a stale Orrery listener on the requested API port.'
    }
    foreach ($Guard in @('must use a non-loopback host', 'must not contain an inline password', 'must target database')) {
      if ($DevScriptText -notmatch [regex]::Escape($Guard)) {
        throw "Remote database validation should enforce '$Guard'."
      }
    }
  }

  It 'keeps process-specific secrets out of the parent PowerShell environment' {
    foreach ($FunctionName in @('Start-ServiceProcess', 'Start-MergedLogServiceProcess')) {
      $FunctionMatch = [regex]::Match($DevScriptText, "function $FunctionName[\s\S]*?\n}\r?\n")
      if (-not $FunctionMatch.Success -or $FunctionMatch.Value -notmatch 'Invoke-WithProcessEnvironment') {
        throw "$FunctionName should scope environment injection to child-process creation."
      }
    }
  }

  It 'keeps local WSL development behind the explicit Local switch' {
    if ($DevScriptText -notmatch '\$LocalDevConfig\s*=\s*Join-Path\s+\$RunRoot\s+''orrery\.wsl\.yaml''') {
      throw 'dev.ps1 should define .dev/orrery.wsl.yaml as the local development config override.'
    }
    if ($DevScriptText -notmatch '\$TrackedWSLDevConfig\s*=\s*Join-Path\s+\$Root\s+''config\\orrery\.wsl-development\.yaml''') {
      throw 'dev.ps1 should define a tracked WSL development config for clean checkouts.'
    }
    $FunctionMatch = [regex]::Match($DevScriptText, 'function Resolve-DefaultDevConfig[\s\S]*?\n}\r?\n')
    if (-not $FunctionMatch.Success -or $FunctionMatch.Value -notmatch '-not \$Local') {
      throw 'Resolve-DefaultDevConfig should select the remote runtime unless Local is explicit.'
    }
    if ($FunctionMatch.Value -notmatch 'return \$LocalDevConfig' -or $FunctionMatch.Value -notmatch 'return \$TrackedWSLDevConfig') {
      throw 'Resolve-DefaultDevConfig should preserve local WSL overrides for Local mode.'
    }
  }

  It 'ships a complete tracked WSL identity development stack' {
    $WSLConfig = Join-Path $PSScriptRoot 'config\orrery.wsl-development.yaml'
    $IdentityRoot = Join-Path $PSScriptRoot 'deploy\dev\identity'
    foreach ($Path in @(
      $WSLConfig,
      (Join-Path $IdentityRoot 'docker-compose.yml'),
      (Join-Path $IdentityRoot 'kratos.yml'),
      (Join-Path $IdentityRoot 'identity.schema.json')
    )) {
      if (-not (Test-Path -LiteralPath $Path)) {
        throw "Tracked WSL identity development asset is missing: $Path"
      }
    }

    $WSLConfigText = Get-Content -Raw -LiteralPath $WSLConfig
    if ($WSLConfigText -notmatch 'auth:\s+[\s\S]*local:\s+[\s\S]*enabled:\s+false') {
      throw 'Tracked WSL config should keep local password login disabled.'
    }
    if ($WSLConfigText -notmatch 'public_url:\s+http://127\.0\.0\.1:4433') {
      throw 'Tracked WSL config should configure the Kratos provider.'
    }
  }

  It 'starts WSL dependencies by default and supports opting out' {
    if ($DevScriptText -notmatch '\[switch\]\$SkipDependencies') {
      throw 'dev.ps1 should expose -SkipDependencies for externally managed development dependencies.'
    }
    if ($DevScriptText -notmatch 'function Ensure-WSLDependencies') {
      throw 'dev.ps1 should define Ensure-WSLDependencies.'
    }
    if ($DevScriptText -notmatch 'function Test-WSLDevConfigSelected') {
      throw 'dev.ps1 should identify tracked and local WSL development configs.'
    }

    $StartMatch = [regex]::Match($DevScriptText, 'function Start-All[\s\S]*?\n}\r?\n\r?\nfunction Stop-All')
    if (-not $StartMatch.Success) {
      throw 'dev.ps1 should define Start-All before Stop-All.'
    }
    $StartText = $StartMatch.Value
    $DependencyIndex = $StartText.IndexOf('Ensure-WSLDependencies')
    $InitializeIndex = $StartText.IndexOf('Initialize-DevDatabases')
    if ($StartText -notmatch '-not \$SkipDependencies') {
      throw 'Start-All should bypass dependency startup only when -SkipDependencies is present.'
    }
    if ($DependencyIndex -lt 0 -or $InitializeIndex -lt 0 -or $DependencyIndex -gt $InitializeIndex) {
      throw 'Start-All should ensure WSL dependencies before database initialization.'
    }
    if ($StartText -notmatch 'Read-ApiRuntimeState') {
      throw 'Start-All should use the recorded API runtime state when deciding whether an existing API needs WSL dependencies.'
    }

    $DependencyMatch = [regex]::Match($DevScriptText, 'function Ensure-WSLDependencies[\s\S]*?\n}\r?\n\r?\nfunction')
    $DependencyText = $DependencyMatch.Value
    foreach ($Expected in @('Ensure-WSLPostgres', 'Invoke-WSLComposeCommand', 'Wait-WSLPostgresReady', 'Wait-HTTPDependencyReady')) {
      if ($DependencyText -notmatch [regex]::Escape($Expected)) {
        throw "Ensure-WSLDependencies should contain '$Expected'."
      }
    }
  }

  It 'starts the configured PostgreSQL container by identity instead of trusting port occupancy' {
    $FunctionMatch = [regex]::Match($DevScriptText, 'function Ensure-WSLPostgres[\s\S]*?\n}\r?\n\r?\nfunction')
    if (-not $FunctionMatch.Success) {
      throw 'dev.ps1 should define Ensure-WSLPostgres.'
    }
    $FunctionText = $FunctionMatch.Value
    foreach ($Expected in @('docker'', ''container'', ''inspect', 'docker'', ''create', 'docker'', ''start')) {
      if ($FunctionText -notmatch [regex]::Escape($Expected)) {
        throw "Ensure-WSLPostgres should contain '$Expected'."
      }
    }
    if ($FunctionText -match 'Test-WSLDependencyPort') {
      throw 'Ensure-WSLPostgres must not skip the configured container because an arbitrary process owns port 5432.'
    }
    if ($FunctionText -notmatch [regex]::Escape('HostConfig.PortBindings')) {
      throw 'Ensure-WSLPostgres should verify that the configured container owns the expected host port mapping.'
    }
  }

  It 'uses service-specific readiness checks for WSL dependencies' {
    if ($DevScriptText -notmatch 'pg_isready') {
      throw 'WSL PostgreSQL readiness should use pg_isready.'
    }
    if ($DevScriptText -notmatch '/health/ready') {
      throw 'Kratos Public API readiness should use its health endpoint.'
    }
    if ($DevScriptText -notmatch [regex]::Escape('$script:OpenFGAApiURL/healthz')) {
      throw 'OpenFGA readiness should use its health endpoint.'
    }
    if ($DevScriptText -match "Wait-HTTPDependencyReady\s+'http://127\.0\.0\.1:4434") {
      throw 'Kratos Admin readiness must not require a host-published port.'
    }
  }

  It 'publishes the Web origin for device browser and WebSocket flows' {
    $FunctionMatch = [regex]::Match($DevScriptText, 'function Write-WSLDevelopmentConfig[\s\S]*?\n}\r?\n')
    if (-not $FunctionMatch.Success) {
      throw 'dev.ps1 should define Write-WSLDevelopmentConfig.'
    }
    if ($FunctionMatch.Value -notmatch [regex]::Escape('device_delivery.public_origin=http://127.0.0.1:$WebPort')) {
      throw 'The rendered development config should use the stable Web origin for device delivery callbacks.'
    }
  }

  It 'provisions a usable Kratos development identity after readiness' {
    if ($DevScriptText -notmatch 'function Ensure-WSLDevelopmentIdentity') {
      throw 'dev.ps1 should define Ensure-WSLDevelopmentIdentity for a clean Kratos datastore.'
    }
    $DependencyMatch = [regex]::Match($DevScriptText, 'function Ensure-WSLDependencies[\s\S]*?\n}\r?\n\r?\nfunction')
    if ($DependencyMatch.Value -notmatch 'Ensure-WSLDevelopmentIdentity') {
      throw 'Ensure-WSLDependencies should provision the development identity after Kratos is ready.'
    }
    foreach ($Expected in @('/admin/identities', 'credentials', 'agent_profile_id', 'Invoke-WSLComposeCommand')) {
      if ($DevScriptText -notmatch [regex]::Escape($Expected)) {
        throw "Kratos development identity provisioning should contain '$Expected'."
      }
    }
    if ($DevScriptText -notmatch [regex]::Escape('Kratos development credentials: username')) {
      throw 'Kratos development startup should print the effective credentials.'
    }
    foreach ($Expected in @('traits.agent_profile_id', 'traits.display_name', 'traits.email', '"$Username@orrery.local"')) {
      if ($DevScriptText -notmatch [regex]::Escape($Expected)) {
        throw "Kratos development identity synchronization should validate '$Expected'."
      }
    }
    foreach ($Expected in @(
      "`$DefaultWSLKratosUsername = 'superadmin'",
      "`$DefaultWSLKratosAgentProfileID = 'human-superadmin'",
      "`$LegacyWSLKratosUsername = 'pm'",
      "`$LegacyWSLKratosAgentProfileID = 'human-pm'",
      'Remove-WSLLegacyDevelopmentIdentity',
      "'kratos', 'list', 'identities'",
      "'kratos', 'delete', 'identity'"
    )) {
      if ($DevScriptText -notmatch [regex]::Escape($Expected)) {
        throw "Kratos superadmin synchronization should contain '$Expected'."
      }
    }
  }

  It 'parses the Kratos CLI identity envelope after diagnostic output' {
    $ParserMatch = [regex]::Match($DevScriptText, 'function ConvertFrom-WSLKratosIdentityOutput[\s\S]*?\n}\r?\n')
    if (-not $ParserMatch.Success) {
      throw 'dev.ps1 should define ConvertFrom-WSLKratosIdentityOutput.'
    }
    foreach ($Expected in @('-split', 'StartsWith', '.identities')) {
      if ($ParserMatch.Value -notmatch [regex]::Escape($Expected)) {
        throw "ConvertFrom-WSLKratosIdentityOutput should contain '$Expected'."
      }
    }
  }

  It 'converts the local compose path before passing it to WSL' {
    if ($DevScriptText -notmatch 'function ConvertTo-WSLPath') {
      throw 'dev.ps1 should convert Windows paths without sending raw backslashes through wsl.exe.'
    }
    $DependencyMatch = [regex]::Match($DevScriptText, 'function Ensure-WSLDependencies[\s\S]*?\n}\r?\n\r?\nfunction')
    if ($DependencyMatch.Value -notmatch '\$IdentityCompose\s*=\s*Resolve-IdentityCompose') {
      throw 'Ensure-WSLDependencies should resolve a local override or tracked compose file.'
    }
    if ($DependencyMatch.Value -notmatch '\$WSLComposePath\s*=\s*ConvertTo-WSLPath\s+\$IdentityCompose') {
      throw 'Ensure-WSLDependencies should convert the resolved compose file path.'
    }
    if ($DependencyMatch.Value -match '--\s+wslpath\b') {
      throw 'Ensure-WSLDependencies should not pass a raw Windows path through wslpath.'
    }
  }

  It 'reports WSL dependency ports in development status' {
    $StatusMatch = [regex]::Match($DevScriptText, 'function Show-Status[\s\S]*?\n}\r?\n\r?\nfunction Start-All')
    if (-not $StatusMatch.Success) {
      throw 'dev.ps1 should define Show-Status before Start-All.'
    }
    if ($StatusMatch.Value -notmatch 'DependencyPortStatus 5432') {
      throw 'Show-Status should report dependency port 5432.'
    }
    foreach ($Expected in @('kratos_public_port', 'openfga_public_port')) {
      if ($StatusMatch.Value -notmatch $Expected) {
        throw "Show-Status should report the effective $Expected from runtime state."
      }
    }
    if ($StatusMatch.Value -match 'DependencyPortStatus 4434') {
      throw 'Show-Status must not report Kratos Admin as a host dependency.'
    }
    if ($StatusMatch.Value -match 'New-ApiEnvironment') {
      throw 'Show-Status must not infer dependency state from the current shell environment.'
    }
    if ($StatusMatch.Value -notmatch 'Read-ApiRuntimeState') {
      throw 'Show-Status should read the environment recorded for the managed API process.'
    }
    foreach ($Expected in @('config_path', 'ORRERY_CONFIG', 'Resolve-DefaultDevConfig', 'Config:')) {
      if ($StatusMatch.Value -notmatch $Expected) {
        throw "Show-Status should report the effective config using '$Expected'."
      }
    }
  }

  It 'does not auto-inject discussion runtime defaults from MiniMax development secrets' {
    if ($DevScriptText -match 'ORRERY_DISCUSSION_AGENT_RUNTIME_CONFIG''\]\s*=\s*Get-ProcessEnvironmentOrDefault') {
      throw 'dev.ps1 should not synthesize ORRERY_DISCUSSION_AGENT_RUNTIME_CONFIG from local MiniMax secrets; settings runtime profiles must stay authoritative.'
    }
    if ($DevScriptText -match 'ORRERY_DISCUSSION_AGENT_RUNTIME_KIND''\]\s*=\s*Get-ProcessEnvironmentOrDefault') {
      throw 'dev.ps1 should not synthesize ORRERY_DISCUSSION_AGENT_RUNTIME_KIND from local MiniMax secrets.'
    }
    if ($DevScriptText -match 'ORRERY_DISCUSSION_AGENT_MODEL''\]\s*=\s*Get-ProcessEnvironmentOrDefault') {
      throw 'dev.ps1 should not synthesize ORRERY_DISCUSSION_AGENT_MODEL from local MiniMax secrets.'
    }

    $DevSh = Join-Path $PSScriptRoot 'dev.sh'
    $DevShText = Get-Content -Raw -LiteralPath $DevSh
    if ($DevShText -match 'ORRERY_DISCUSSION_AGENT_RUNTIME_CONFIG=%q\\n'' "\$\{ORRERY_DISCUSSION_AGENT_RUNTIME_CONFIG:-') {
      throw 'dev.sh should not synthesize ORRERY_DISCUSSION_AGENT_RUNTIME_CONFIG from local MiniMax secrets.'
    }
    if ($DevShText -match 'ORRERY_DISCUSSION_AGENT_RUNTIME_KIND=%q\\n'' "\$\{ORRERY_DISCUSSION_AGENT_RUNTIME_KIND:-') {
      throw 'dev.sh should not synthesize ORRERY_DISCUSSION_AGENT_RUNTIME_KIND from local MiniMax secrets.'
    }
    if ($DevShText -match 'ORRERY_DISCUSSION_AGENT_MODEL=%q\\n'' "\$\{ORRERY_DISCUSSION_AGENT_MODEL:-') {
      throw 'dev.sh should not synthesize ORRERY_DISCUSSION_AGENT_MODEL from local MiniMax secrets.'
    }
  }

  It 'cleans up every listener on service ports during restart stop' {
    if ($DevScriptText -notmatch 'function Stop-PortOwners') {
      throw 'dev.ps1 should define Stop-PortOwners so restart can clean all listener processes on a port.'
    }

    $FunctionMatch = [regex]::Match($DevScriptText, 'function Stop-ServiceProcess\([\s\S]*?\n}\r?\n\r?\nfunction Stop-ClaudeAgentSDKSidecars')
    if (-not $FunctionMatch.Success) {
      throw 'dev.ps1 should define Stop-ServiceProcess before Stop-ClaudeAgentSDKSidecars.'
    }
    if ($FunctionMatch.Value -notmatch 'Stop-PortOwners') {
      throw 'Stop-ServiceProcess should call Stop-PortOwners after stopping the pid-file process.'
    }
  }

  It 'uses operating-system assigned loopback ports when implicit defaults are unavailable' {
    if ($DevScriptText -notmatch 'function Get-RandomLoopbackPort') {
      throw 'dev.ps1 should define Get-RandomLoopbackPort.'
    }
    $FunctionMatch = [regex]::Match($DevScriptText, 'function Get-RandomLoopbackPort[\s\S]*?\n}\r?\n')
    foreach ($Expected in @('TcpListener', 'IPAddress]::Loopback', 'LocalEndpoint', 'Stop()')) {
      if ($FunctionMatch.Value -notmatch [regex]::Escape($Expected)) {
        throw "Get-RandomLoopbackPort should contain '$Expected'."
      }
    }
    foreach ($Expected in @('Resolve-EffectiveApiAddress', 'Resolve-KratosPublicPort', 'Resolve-OpenFGAPublicPort')) {
      if ($DevScriptText -notmatch "function $Expected") {
        throw "dev.ps1 should define $Expected."
      }
    }
  }

  It 'selects a random OpenFGA port whenever the default port is occupied' {
    $FunctionMatch = [regex]::Match($DevScriptText, 'function Resolve-OpenFGAPublicPort[\s\S]*?\n}\r?\n')
    if (-not $FunctionMatch.Success) {
      throw 'dev.ps1 should define Resolve-OpenFGAPublicPort.'
    }
    $FunctionText = $FunctionMatch.Value
    if ($FunctionText -notmatch [regex]::Escape('if (-not (Test-PortListening $OpenFGADefaultPort))')) {
      throw 'Resolve-OpenFGAPublicPort should use the default only when it is not listening.'
    }
    if ($FunctionText -match 'Test-HTTPReady') {
      throw 'Resolve-OpenFGAPublicPort must not treat an arbitrary HTTP response as an available port.'
    }
    if ($FunctionText -notmatch [regex]::Escape('Get-RandomLoopbackPort')) {
      throw 'Resolve-OpenFGAPublicPort should allocate a random loopback port when the default is occupied.'
    }
  }

  It 'records effective random endpoints and uses them for status and stop' {
    $WriteStateMatch = [regex]::Match($DevScriptText, 'function Write-ApiRuntimeState[\s\S]*?\n}\r?\n')
    foreach ($Expected in @('api_addr', 'kratos_public_port', 'openfga_public_port', 'wsl_distro')) {
      if ($WriteStateMatch.Value -notmatch $Expected) {
        throw "API runtime state should record $Expected."
      }
    }
    $StopMatch = [regex]::Match($DevScriptText, 'function Stop-All[\s\S]*?\n}\r?\n')
    if ($StopMatch.Value -notmatch 'api_addr') {
      throw 'Stop-All should use the recorded effective API address.'
    }
  }

  It 'keeps WSL alive while managed development services run' {
    foreach ($Expected in @('$WSLKeepAlivePidFile', 'function Ensure-WSLKeepAlive', 'function Stop-WSLKeepAlive')) {
      if ($DevScriptText -notmatch [regex]::Escape($Expected)) {
        throw "dev.ps1 should contain '$Expected'."
      }
    }
    $KeepAliveMatch = [regex]::Match($DevScriptText, 'function Ensure-WSLKeepAlive[\s\S]*?\n}\r?\n')
    foreach ($Expected in @('Start-Process', 'wsl.exe', 'tail', '/dev/null', 'WindowStyle Hidden')) {
      if ($KeepAliveMatch.Value -notmatch [regex]::Escape($Expected)) {
        throw "Ensure-WSLKeepAlive should contain '$Expected'."
      }
    }
    $DependencyMatch = [regex]::Match($DevScriptText, 'function Ensure-WSLDependencies[\s\S]*?\n}\r?\n\r?\nfunction')
    if ($DependencyMatch.Value.IndexOf('Ensure-WSLKeepAlive') -gt $DependencyMatch.Value.IndexOf('Ensure-WSLPostgres')) {
      throw 'Ensure-WSLDependencies should keep WSL alive before starting PostgreSQL.'
    }
  }

  It 'keeps Kratos Admin internal and parameterizes only the public port' {
    $TrackedCompose = Join-Path $PSScriptRoot 'deploy\dev\identity\docker-compose.yml'
    $ComposePaths = @($TrackedCompose)
    $LocalCompose = Join-Path $PSScriptRoot '.dev\identity\docker-compose.yml'
    if (Test-Path -LiteralPath $LocalCompose) {
      $ComposePaths += $LocalCompose
    }
    foreach ($ComposePath in $ComposePaths) {
      $ComposeText = Get-Content -Raw -LiteralPath $ComposePath
      if ($ComposeText -notmatch [regex]::Escape('127.0.0.1:${ORRERY_KRATOS_PUBLIC_PORT:-4433}:4433')) {
        throw "$ComposePath should parameterize the loopback Kratos Public API port."
      }
      if ($ComposeText -notmatch 'SERVE_PUBLIC_BASE_URL') {
        throw "$ComposePath should configure the matching Kratos Public API base URL."
      }
      if ($ComposeText -match '4434:4434') {
        throw "$ComposePath must not publish Kratos Admin API port 4434."
      }
    }
  }

  It 'parameterizes the OpenFGA loopback port and records its effective endpoint' {
    $TrackedCompose = Join-Path $PSScriptRoot 'deploy\dev\identity\docker-compose.yml'
    $ComposeText = Get-Content -Raw -LiteralPath $TrackedCompose
    if ($ComposeText -notmatch [regex]::Escape('127.0.0.1:${ORRERY_OPENFGA_PUBLIC_PORT:-8082}:8080')) {
      throw 'The development identity Compose file should parameterize the loopback OpenFGA port.'
    }
    foreach ($Expected in @('ORRERY_OPENFGA_PUBLIC_PORT', '$script:OpenFGAApiURL', 'openfga_public_port')) {
      if ($DevScriptText -notmatch [regex]::Escape($Expected)) {
        throw "dev.ps1 should contain '$Expected'."
      }
    }
  }
}
