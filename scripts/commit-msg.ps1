# Write a commit message to a file with no BOM, for `git commit -F`.
#
# PowerShell 5.1's `Out-File -Encoding utf8` writes a BOM, and `git commit -F`
# reads the first byte as part of the subject. The result is a commit whose
# subject begins with U+FEFF, which renders as an invisible character at the
# start of `git log --oneline` and breaks any tooling that matches on the
# conventional-commit prefix.
#
# Usage:  .\scripts\commit-msg.ps1 <file>   then   git commit -F <file>
param(
  [Parameter(Mandatory = $true)][string]$Path
)

$content = [System.IO.File]::ReadAllText($Path)
# Strip a BOM if the editor or a previous shell added one.
$content = $content.TrimStart([char]0xFEFF)
[System.IO.File]::WriteAllText($Path, $content, (New-Object System.Text.UTF8Encoding($false)))

$bytes = [System.IO.File]::ReadAllBytes($Path)
if ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) {
  throw "commit message still carries a BOM"
}
Write-Output "ok: $Path ($($bytes.Length) bytes, no BOM)"