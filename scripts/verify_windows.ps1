# verify_windows.ps1 - fail-fast local verification gate for gov8.
#
# Mirrors the .github/workflows/windows-amd64.yml gates so local runs and CI
# execute exactly the same checks in the same order:
#
#   go gate     gofmt check (list + fail), go vet ./..., go test ./...,
#               every conformance suite re-executed with -count=1, and
#               go test -race ./... when the platform supports it
#               (on windows/amd64 the race detector requires CGO with a
#               gcc/mingw toolchain for the external link step; otherwise
#               the gate reports a precise skip reason).
#   rust gate   cargo fmt --all -- --check, cargo check --locked
#               --all-targets, cargo clippy --locked --all-targets with
#               warnings denied, cargo test --locked - always on the pinned
#               toolchain declared by rust-oracle/rust-toolchain.toml.
#   bench gate  benchmark smoke only: every Go benchmark runs exactly once
#               (-benchtime=1x) and every Rust criterion benchmark runs in
#               test mode (--test, one pass, no measurement). Timings from
#               this gate are meaningless by construction and are never
#               treated as performance evidence.
#
# The shim DLL must exist before the go/bench gates (it is what the tests
# load); pass -RunSetup to invoke scripts/setup_windows.ps1 automatically.
#
# Usage:
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify_windows.ps1
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify_windows.ps1 -Gates go,rust
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify_windows.ps1 -RunSetup
#
# Fail fast: the first failing gate stops the script with a non-zero exit.

param(
    # Comma-separated subset of gates to run: go, rust, bench. Default: all.
    [string]$Gates = 'go,rust,bench',
    # Run scripts/setup_windows.ps1 automatically when the shim DLL is missing.
    [switch]$RunSetup
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2

$Root = Split-Path -Parent $PSScriptRoot
$ShimDll = Join-Path $Root 'build\shim\gov8_shim.dll'
$SetupScript = Join-Path $Root 'scripts\setup_windows.ps1'
$OracleDir = Join-Path $Root 'rust-oracle'
$ExpectedRustToolchain = '1.98.0'

$ConformancePackages = @(
    './conformance',
    './conformance-buffers',
    './conformance-controls-hooks',
    './conformance-core-advanced',
    './conformance-context-residual',
    './conformance-context-scopes-advanced',
    './conformance-create-params-snapshot',
    './conformance-exceptions-advanced',
    './conformance-exception-constructors',
    './conformance-exception-string-local',
    './conformance-external-references',
    './conformance-fast-api-substrate',
    './conformance-functions-advanced',
    './conformance-inspector-transport',
    './conformance-inspector-session-controls',
    './conformance-fixed-primitive-arrays',
    './conformance-handles-residual',
    './conformance-host-promises',
    './conformance-host-templates',
    './conformance-isolate-advanced',
    './conformance-icu',
    './conformance-message-locals',
    './conformance-module-advanced-residual',
    './conformance-module-cache',
    './conformance-modules',
    './conformance-modules-synthetic',
    './conformance-object-ops',
    './conformance-object-callback-retention',
    './conformance-object-residual',
    './conformance-platform',
    './conformance-platform-custom',
    './conformance-runtime-values',
    './conformance-runtime-values-residual',
    './conformance-serializer-delegates',
    './conformance-serializer-wasm-legacy-residual',
    './conformance-script-compiler-residual',
    './conformance-simdutf',
    './conformance-snapshots',
    './conformance-strings-bigint',
    './conformance-template-advanced',
    './conformance-typed-arrays',
    './conformance-trycatch-listener-residual',
    './conformance-wasm',
    './conformance-wasm-policy-callbacks',
    './conformance-wasm-streaming'
)

$script:RaceSupported = $null
$script:RaceSkipReason = $null

# --- helpers ------------------------------------------------------------------
function Use-Location([string]$Path, [scriptblock]$Body) {
    if (-not (Test-Path -LiteralPath $Path)) { throw "directory not found: $Path" }
    Push-Location -LiteralPath $Path
    try {
        & $Body
    } finally {
        Pop-Location
    }
}

function Assert-LastExit([string]$What) {
    if ($LASTEXITCODE -ne 0) {
        throw "$What failed with exit code $LASTEXITCODE"
    }
}

function Test-RaceSupported {
    # go test -race on windows/amd64 requires CGO and a C toolchain (the race
    # runtime is linked externally). Probe the real conditions instead of
    # assuming; report a precise skip reason when unsupported.
    if ($null -ne $script:RaceSupported) { return $script:RaceSupported }
    $goos = (& go env GOOS)
    $goarch = (& go env GOARCH)
    $cgo = (& go env CGO_ENABLED)
    if ($goos -ne 'windows' -or $goarch -ne 'amd64') {
        $script:RaceSkipReason = "race target is windows/amd64, found GOOS=$goos GOARCH=$goarch"
        $script:RaceSupported = $false
        return $false
    }
    if ("$cgo" -ne '1') {
        $script:RaceSkipReason = 'CGO_ENABLED=0 (the race runtime requires CGO on Windows)'
        $script:RaceSupported = $false
        return $false
    }
    $cc = (& go env CC)
    if (-not $cc -or -not (Get-Command $cc -ErrorAction SilentlyContinue)) {
        $script:RaceSkipReason = "no C compiler for CGO ('go env CC' resolves to '$cc'); Windows -race needs gcc/mingw"
        $script:RaceSupported = $false
        return $false
    }
    $script:RaceSkipReason = $null
    $script:RaceSupported = $true
    return $true
}

# --- gates --------------------------------------------------------------------
function Invoke-Preflight([bool]$NeedsShim) {
    foreach ($tool in @('go', 'gofmt', 'cargo')) {
        if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) {
            throw "required tool not found on PATH: $tool"
        }
    }
    $goos = (& go env GOOS)
    $goarch = (& go env GOARCH)
    if ($goos -ne 'windows' -or $goarch -ne 'amd64') {
        throw "gov8 supports only GOOS=windows GOARCH=amd64 (found GOOS=$goos GOARCH=$goarch)"
    }
    Write-Host "[preflight] go:    $(& go version)"
    Write-Host "[preflight] cargo: $(& cargo --version)"
    if (-not $NeedsShim) { return }
    if (Test-Path -LiteralPath $ShimDll) { return }
    if ($RunSetup) {
        Write-Host '[preflight] shim DLL missing; running scripts/setup_windows.ps1 ...'
        & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $SetupScript
        Assert-LastExit 'scripts/setup_windows.ps1'
    }
    if (-not (Test-Path -LiteralPath $ShimDll)) {
        throw "shim DLL not found: $ShimDll - run scripts/setup_windows.ps1 first (or pass -RunSetup)"
    }
}

function Invoke-GoGate {
    Use-Location $Root {
        Write-Host '[go] gofmt -l .'
        $unformatted = @(& gofmt -l .)
        Assert-LastExit 'gofmt'
        if ($unformatted.Count -gt 0) {
            $unformatted | ForEach-Object { Write-Host "  not gofmt-ed: $_" }
            throw 'gofmt check failed: run gofmt -w on the files listed above'
        }

        Write-Host '[go] go vet ./...'
        & go vet ./...
        Assert-LastExit 'go vet ./...'

        Write-Host '[go] go test ./...'
        & go test ./...
        Assert-LastExit 'go test ./...'

        Write-Host '[go] conformance suites (explicit, -count=1)'
        & go test -count=1 $ConformancePackages
        Assert-LastExit 'conformance suites'

        if (Test-RaceSupported) {
            Write-Host '[go] go test -race ./...'
            & go test -race ./...
            Assert-LastExit 'go test -race ./...'
        } else {
            Write-Host "[go] skipping -race: $($script:RaceSkipReason)"
        }
    }
}

function Invoke-RustGate {
    Use-Location $OracleDir {
        $rustcVersion = (& rustc --version)
        Write-Host "[rust] toolchain: $rustcVersion"
        if ($rustcVersion -notmatch ('^rustc ' + [regex]::Escape($ExpectedRustToolchain) + '\b')) {
            throw ("rust-oracle must build with rustc {0} (rust-oracle/rust-toolchain.toml); found: {1}. " +
                   'Install it with: rustup toolchain install {0}') -f $ExpectedRustToolchain, $rustcVersion
        }

        Write-Host '[rust] cargo fmt --all -- --check'
        & cargo fmt --all -- --check
        Assert-LastExit 'cargo fmt --check'

        Write-Host '[rust] cargo check --locked --all-targets'
        & cargo check --locked --all-targets
        Assert-LastExit 'cargo check'

        Write-Host '[rust] cargo clippy --locked --all-targets -- -D warnings'
        & cargo clippy --locked --all-targets -- -D warnings
        Assert-LastExit 'cargo clippy'

        Write-Host '[rust] cargo test --locked'
        & cargo test --locked
        Assert-LastExit 'cargo test'
    }
}

function Invoke-BenchGate {
    Write-Host '[bench] smoke only - timings from this gate are not performance evidence'
    Use-Location $Root {
        Write-Host '[bench] go: go test -run ^$ -bench . -benchtime 1x ./...'
        & go test -run '^$' -bench . -benchtime 1x ./...
        Assert-LastExit 'go benchmark smoke'
    }
    Use-Location $OracleDir {
        Write-Host '[bench] rust: cargo bench --locked -- --test (criterion test mode: one pass, no measurement)'
        & cargo bench --locked -- --test
        Assert-LastExit 'cargo benchmark smoke'
    }
}

# --- main ---------------------------------------------------------------------
$AllGates = @('go', 'rust', 'bench')
$requested = @($Gates -split ',' | ForEach-Object { $_.Trim().ToLowerInvariant() } | Where-Object { $_ })
if ($requested.Count -eq 0) {
    throw "no gates requested ('$Gates'); valid gates: $($AllGates -join ', ')"
}
$unknown = @($requested | Where-Object { $AllGates -notcontains $_ })
if ($unknown.Count -gt 0) {
    throw "unknown gate(s): $($unknown -join ', '); valid gates: $($AllGates -join ', ')"
}

# Only the go and bench gates execute Go test binaries, which load the shim.
$NeedsShim = ($requested -contains 'go') -or ($requested -contains 'bench')

try {
    Invoke-Preflight $NeedsShim
    if ($requested -contains 'go') { Invoke-GoGate }
    if ($requested -contains 'rust') { Invoke-RustGate }
    if ($requested -contains 'bench') { Invoke-BenchGate }
} catch {
    Write-Host ''
    Write-Host ("VERIFY FAILED: {0}" -f $_.Exception.Message)
    Write-Host $_.ScriptStackTrace
    exit 1
}

Write-Host ''
Write-Host ("VERIFY PASSED (gates: {0})" -f ($requested -join ', '))
exit 0
