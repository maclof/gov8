# Captures machine/environment metadata for benchmark runs.
# Windows PowerShell 5.1 (powershell.exe); no PowerShell Core dependency.
#
# Usage (Windows PowerShell, from the rust-oracle directory):
#   powershell -NoProfile -ExecutionPolicy Bypass -File scripts\capture-env.ps1 > bench-results\env-<date>-<host>.txt
#
# This file is committed with the benchmark raw output so results can be
# interpreted and compared over time. It contains no secrets.

Write-Output "=== gov8 rust-oracle benchmark environment ==="
Write-Output "captured_at_utc : $((Get-Date).ToUniversalTime().ToString('o'))"

Write-Output "`n--- OS ---"
$os = Get-CimInstance Win32_OperatingSystem
Write-Output "caption         : $($os.Caption)"
Write-Output "version         : $($os.Version)"
Write-Output "build           : $($os.BuildNumber)"
Write-Output "os_architecture : $($os.OSArchitecture)"
Write-Output "last_boot       : $($os.LastBootUpTime)"

Write-Output "`n--- CPU ---"
$cpu = Get-CimInstance Win32_Processor | Select-Object -First 1
Write-Output "name            : $($cpu.Name.Trim())"
Write-Output "cores           : $($cpu.NumberOfCores)"
Write-Output "logical         : $($cpu.NumberOfLogicalProcessors)"
Write-Output "max_clock_mhz   : $($cpu.MaxClockSpeed)"

Write-Output "`n--- RAM ---"
$totalGb = [math]::Round($os.TotalVisibleMemorySize / 1MB, 2)
$freeGb = [math]::Round($os.FreePhysicalMemory / 1MB, 2)
Write-Output "total_gb        : $totalGb"
Write-Output "free_gb         : $freeGb"

Write-Output "`n--- Toolchain ---"
Write-Output "rustc           : $(rustc --version 2>$null)"
Write-Output "cargo           : $(cargo --version 2>$null)"
Write-Output "rustup_show     :"
rustup show 2>$null | ForEach-Object { Write-Output "  $_" }

Write-Output "`n--- Pinned oracle versions ---"
$cargoToml = Get-Content (Join-Path $PSScriptRoot "..\Cargo.toml") -Raw
$v8Line = ($cargoToml -split "`n" | Where-Object { $_ -match '^\s*v8\s*=' }) -join ""
$critLine = ($cargoToml -split "`n" | Where-Object { $_ -match '^\s*criterion\s*=' }) -join ""
Write-Output "v8_crate_pin    : $($v8Line.Trim())"
Write-Output "criterion_pin   : $($critLine.Trim())"
Write-Output "cargo_config    :"
Get-Content (Join-Path $PSScriptRoot "..\.cargo\config.toml") | ForEach-Object { Write-Output "  $_" }
