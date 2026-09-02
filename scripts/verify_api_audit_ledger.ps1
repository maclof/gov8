param(
    [switch]$Regenerate,
    [string]$LedgerPath = "audit/v8_152_2_0_declarations.csv"
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$repo = Split-Path -Parent $PSScriptRoot
$ledger = Join-Path $repo $LedgerPath
$target = Join-Path $repo "build/api-audit-ledger"
$jsonPath = Join-Path $target "doc/v8.json"

$oldBootstrap = $env:RUSTC_BOOTSTRAP
try {
    $env:RUSTC_BOOTSTRAP = "1"
    & cargo rustdoc --manifest-path (Join-Path $repo "rust-oracle/Cargo.toml") --locked -p v8 --target-dir $target -- -Z unstable-options --output-format json
    if ($LASTEXITCODE -ne 0) {
        throw "cargo rustdoc failed with exit code $LASTEXITCODE"
    }
} finally {
    $env:RUSTC_BOOTSTRAP = $oldBootstrap
}

$doc = Get-Content -LiteralPath $jsonPath -Raw | ConvertFrom-Json
if ($doc.crate_version -ne "152.2.0") {
    throw "expected v8 rustdoc 152.2.0, got $($doc.crate_version)"
}
if ($doc.target.triple -ne "x86_64-pc-windows-msvc") {
    throw "expected x86_64-pc-windows-msvc rustdoc, got $($doc.target.triple)"
}
if ($doc.format_version -ne 60 -or $doc.includes_private) {
    throw "expected public-only rustdoc JSON format 60"
}

$index = @{}
foreach ($property in $doc.index.PSObject.Properties) {
    $index[$property.Name] = $property.Value
}
$paths = @{}
foreach ($property in $doc.paths.PSObject.Properties) {
    $paths[$property.Name] = $property.Value
}

function Get-ItemKind($item) {
    return [string](($item.inner.PSObject.Properties | Select-Object -First 1).Name)
}

function Get-RelativeSource($item) {
    $filename = [string]$item.span.filename
    $marker = "\v8-152.2.0\src\"
    $offset = $filename.IndexOf($marker, [StringComparison]::OrdinalIgnoreCase)
    if ($offset -ge 0) {
        return "src/" + $filename.Substring($offset + $marker.Length).Replace("\", "/")
    }
    $leaf = [IO.Path]::GetFileName($filename)
    if ($leaf -like "src_binding_*.rs") {
        return "generated/$leaf"
    }
    return $null
}

# Rustdoc does not attach a canonical path to associated items. Build the
# owner relation from impl, trait, enum, and struct membership instead of
# relying on compiler-internal item IDs.
$owners = @{}
foreach ($item in $index.Values) {
    if ($item.crate_id -ne 0) { continue }
    $kind = Get-ItemKind $item
    switch ($kind) {
        "impl" {
            if ($item.inner.impl.for.PSObject.Properties.Name -contains "resolved_path") {
                $owner = [string]$item.inner.impl.for.resolved_path.path
                foreach ($child in $item.inner.impl.items) {
                    $owners[[string]$child] = $owner
                }
            }
        }
        "trait" {
            foreach ($child in $item.inner.trait.items) {
                $owners[[string]$child] = [string]$item.name
            }
        }
        "enum" {
            foreach ($child in $item.inner.enum.variants) {
                $owners[[string]$child] = [string]$item.name
            }
        }
        "struct" {
            if ($item.inner.struct.kind.PSObject.Properties.Name -contains "plain") {
                foreach ($child in $item.inner.struct.kind.plain.fields) {
                    $owners[[string]$child] = [string]$item.name
                }
            }
            if ($item.inner.struct.kind.PSObject.Properties.Name -contains "tuple") {
                foreach ($child in $item.inner.struct.kind.tuple) {
                    if ($null -ne $child) { $owners[[string]$child] = [string]$item.name }
                }
            }
        }
        "union" {
            foreach ($child in $item.inner.union.fields) {
                $owners[[string]$child] = [string]$item.name
            }
        }
    }
}

function Get-RustPath($item, [string]$source, [string]$kind) {
    $id = [string]$item.id
    $module = [IO.Path]::GetFileNameWithoutExtension($source)
    $publicModules = @("cppgc", "crdtp", "fast_api", "icu", "inspector", "json", "script_compiler", "simdutf", "V8")
    if ($paths.ContainsKey($id)) {
        return (($paths[$id].path | ForEach-Object { [string]$_ }) -join "::")
    }
    if ($owners.ContainsKey($id)) {
        $owner = $owners[$id].Replace("crate::", "v8::")
        if (-not $owner.StartsWith("v8::")) {
            if ($module -in $publicModules) { $owner = "v8::$module::$owner" }
            else { $owner = "v8::$owner" }
        }
        return "$owner::$($item.name)"
    }
    if ($module -in $publicModules) {
        return "v8::$module::$($item.name)"
    }
    return "v8::$($item.name)"
}

$selected = @{}
function Add-Selected($item) {
    $id = [string]$item.id
    if ($selected.ContainsKey($id)) { return }
    $source = Get-RelativeSource $item
    if ($null -eq $source) { return }
    $kind = Get-ItemKind $item
    # The generated UseCounterFeature variants are bindgen implementation
    # noise, not rusty_v8 declarations. The small generated wrapper types,
    # fields, and constants are retained because rusty_v8 publicly reexports
    # and documents them.
    if ($source -like "generated/src_binding_*.rs" -and $kind -eq "variant") { return }
    $rustPath = Get-RustPath $item $source $kind
    $line = [int]$item.span.begin[0]
    $stable = "$source`:$line`:$kind`:$rustPath"
    $selected[$id] = [pscustomobject][ordered]@{
        id = $stable
        family = $source
        rust_path = $rustPath
        kind = $kind
        source = $source
        line = $line
    }
}

$publicKinds = @("function", "struct", "enum", "trait", "type_alias", "constant", "static", "union", "macro", "module", "struct_field", "assoc_const", "assoc_type")
foreach ($item in $index.Values) {
    if ($item.crate_id -ne 0 -or $null -eq $item.span) { continue }
    $source = Get-RelativeSource $item
    if ($null -eq $source) { continue }
    if ($source -eq "src/binding.rs") { continue }
    $kind = Get-ItemKind $item
    if ($item.visibility -eq "public" -and $kind -in $publicKinds) {
        Add-Selected $item
    }
}
foreach ($item in $index.Values) {
    if ($item.crate_id -ne 0 -or $null -eq $item.span) { continue }
    $source = Get-RelativeSource $item
    if ($null -eq $source) { continue }
    if ($source -eq "src/binding.rs") { continue }
    $kind = Get-ItemKind $item
    if ($item.visibility -eq "public" -and $kind -eq "trait") {
        foreach ($child in $item.inner.trait.items) { Add-Selected $index[[string]$child] }
    }
    if ($item.visibility -eq "public" -and $kind -eq "enum") {
        foreach ($child in $item.inner.enum.variants) { Add-Selected $index[[string]$child] }
    }
}

$projection = @($selected.Values)
# Rustdoc omits inherent methods on this generated type alias.
$projection += [pscustomobject][ordered]@{
    id = "src/fast_api.rs:192:function:v8::fast_api::FastApiOneByteString::as_bytes"
    family = "src/fast_api.rs"
    rust_path = "v8::fast_api::FastApiOneByteString::as_bytes"
    kind = "function"
    source = "src/fast_api.rs"
    line = 192
}
$projection = @($projection | Sort-Object source, line, kind, rust_path)

if (-not (Test-Path -LiteralPath $ledger)) {
    throw "ledger is missing: $ledger"
}
$rows = @(Import-Csv -LiteralPath $ledger)

$expectedColumns = @("id", "family", "rust_path", "kind", "source", "line", "classification", "go_mapping", "evidence", "rationale")
foreach ($column in $expectedColumns) {
    if (-not ($rows[0].PSObject.Properties.Name -contains $column)) {
        throw "ledger is missing column '$column'"
    }
}

$byID = @{}
foreach ($row in $rows) {
    if ($byID.ContainsKey($row.id)) { throw "duplicate ledger ID: $($row.id)" }
    $byID[$row.id] = $row
}
$projectionByID = @{}
foreach ($row in $projection) {
    if ($projectionByID.ContainsKey($row.id)) { throw "non-unique generated stable ID: $($row.id)" }
    $projectionByID[$row.id] = $row
}

$missing = @($projection | Where-Object { -not $byID.ContainsKey($_.id) })
$stale = @($rows | Where-Object { -not $projectionByID.ContainsKey($_.id) })
if ($missing.Count -or $stale.Count) {
    throw "ledger/source mismatch: $($missing.Count) missing rows, $($stale.Count) stale rows"
}

foreach ($row in $rows) {
    $sourceRow = $projectionByID[$row.id]
    foreach ($column in @("family", "rust_path", "kind", "source", "line")) {
        if ([string]$row.$column -ne [string]$sourceRow.$column) {
            throw "$($row.id): '$column' is '$($row.$column)', expected '$($sourceRow.$column)'"
        }
    }
    if ($row.classification -notin @("matched", "partial", "unsafe")) {
        throw "$($row.id): invalid classification '$($row.classification)'"
    }
    foreach ($column in @("go_mapping", "evidence", "rationale")) {
        if ([string]::IsNullOrWhiteSpace([string]$row.$column)) {
            throw "$($row.id): '$column' must not be blank"
        }
    }
    if ($row.classification -ne "unsafe") {
        foreach ($reference in ([string]$row.go_mapping -split ";")) {
            $reference = $reference.Trim()
            if (-not (Test-Path -LiteralPath (Join-Path $repo $reference))) {
                throw "$($row.id): Go mapping does not exist: $reference"
            }
        }
    }
    foreach ($reference in ([string]$row.evidence -split ";")) {
        $reference = $reference.Trim()
        $file = ($reference -split "#", 2)[0]
        if (-not (Test-Path -LiteralPath (Join-Path $repo $file))) {
            throw "$($row.id): evidence does not exist: $reference"
        }
    }
}

$counts = @{}
foreach ($group in ($rows | Group-Object classification)) { $counts[$group.Name] = $group.Count }
$expected = @{ matched = 1689; partial = 19; unsafe = 149 }
foreach ($classification in $expected.Keys) {
    if ($counts[$classification] -ne $expected[$classification]) {
        throw "$classification count is $($counts[$classification]), expected $($expected[$classification])"
    }
}
if ($rows.Count -ne 1857) { throw "ledger total is $($rows.Count), expected 1857" }

$expectedPartialPaths = @(
    "v8::cppgc::Visitor",
    "v8::cppgc::Visitor::trace",
    "v8::cppgc::Traced",
    "v8::cppgc::Traced::trace",
    "v8::cppgc::InternalFieldIndex",
    "v8::cppgc::UnsafePtr",
    "v8::cppgc::UnsafePtr::new",
    "v8::cppgc::UnsafePtr::as_ref",
    "v8::cppgc::Member",
    "v8::cppgc::WeakMember",
    "v8::Object::get_creation_context",
    "v8::platform::PlatformImpl",
    "v8::PlatformImpl::post_task",
    "v8::PlatformImpl::post_non_nestable_task",
    "v8::PlatformImpl::post_delayed_task",
    "v8::PlatformImpl::post_non_nestable_delayed_task",
    "v8::PlatformImpl::post_idle_task",
    "v8::PinnedRef::get_current_context",
    "v8::PinnedRef::get_entered_or_microtask_context"
) | Sort-Object
$actualPartialPaths = @($rows | Where-Object { $_.classification -eq "partial" } | ForEach-Object { $_.rust_path } | Sort-Object)
if (($expectedPartialPaths -join "`n") -ne ($actualPartialPaths -join "`n")) {
    throw "the 19 reviewed partial declarations changed"
}

$expectedUnsafeRationales = @{
    "Rust pinning, lexical macro, or generic scope-construction machinery" = 49
    "Rust-only generic smart-pointer or mapping-vtable machinery" = 32
    "Rust raw local/global handle or unchecked lifetime machinery" = 21
    "Rust raw isolate-pointer or manual enter/exit shape" = 15
    "Generated raw ABI layout, not a public Go ownership surface" = 10
    "Raw allocator vtable or caller-owned backing pointer" = 9
    "Callback-borrowed Fast API ABI shape; behavior is native and copied in Go" = 6
    "Raw Inspector base/vtable wrapper or borrowed iterator" = 4
    "Raw native stack-pointer configuration" = 2
    "Rust SharedRef platform ownership shape" = 1
}
$actualUnsafeRationales = @{}
foreach ($group in ($rows | Where-Object { $_.classification -eq "unsafe" } | Group-Object rationale)) {
    $actualUnsafeRationales[$group.Name] = $group.Count
}
foreach ($rationale in $expectedUnsafeRationales.Keys) {
    if ($actualUnsafeRationales[$rationale] -ne $expectedUnsafeRationales[$rationale]) {
        throw "unsafe rationale '$rationale' count changed"
    }
}
if ($actualUnsafeRationales.Count -ne $expectedUnsafeRationales.Count) {
    throw "an unreviewed unsafe rationale was added"
}

if ($Regenerate) {
    $ordered = foreach ($sourceRow in $projection) {
        $old = $byID[$sourceRow.id]
        [pscustomobject][ordered]@{
            id = $sourceRow.id
            family = $sourceRow.family
            rust_path = $sourceRow.rust_path
            kind = $sourceRow.kind
            source = $sourceRow.source
            line = $sourceRow.line
            classification = $old.classification
            go_mapping = $old.go_mapping
            evidence = $old.evidence
            rationale = $old.rationale
        }
    }
    $directory = Split-Path -Parent $ledger
    New-Item -ItemType Directory -Path $directory -Force | Out-Null
    $ordered | Export-Csv -LiteralPath $ledger -NoTypeInformation -Encoding UTF8
}

Write-Host "API audit ledger verified: 1857 declarations (1689 matched, 19 partial, 149 intentional-shape/unsafe-status)."
