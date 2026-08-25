# BizVerse backup & restore
# Backup:  ./scripts/backup.ps1 -DatabaseUrl "postgres://..." -OutDir ./backups
# Restore: ./scripts/backup.ps1 -DatabaseUrl "postgres://..." -Restore ./backups/bizverse-YYYY-MM-DD.sql.gz
#
# Prefer the DATABASE_URL environment variable over the parameter: values
# passed on the command line can leak into process listings / shell history.

param(
    [string]$DatabaseUrl = $env:DATABASE_URL,
    [string]$OutDir = "./backups",
    [string]$Restore = ""
)

if (-not $DatabaseUrl) { Write-Error "DATABASE_URL is required"; exit 1 }

New-Item -ItemType Directory -Path $OutDir -Force | Out-Null

if ($Restore) {
    Write-Host "Restoring from $Restore ..."
    if ($Restore -like '*.gz') {
        & gzip -dc $Restore | & psql --dbname=$DatabaseUrl
    } else {
        & psql --dbname=$DatabaseUrl -f $Restore
    }
    if ($LASTEXITCODE -ne 0) { Write-Error "Restore failed"; exit 1 }
    Write-Host "Restore complete."
    exit 0
}

$stamp = Get-Date -Format 'yyyy-MM-dd'
$file = Join-Path $OutDir "bizverse-$stamp.sql.gz"
& pg_dump --dbname=$DatabaseUrl | & gzip > $file
if ($LASTEXITCODE -ne 0) { Write-Error "Backup failed"; exit 1 }
Write-Host "Backup written: $file"
Write-Host "Verify with a restore into a scratch DB quarterly (docs/DEPLOYMENT.md)."
