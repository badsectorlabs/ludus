# Don't stop if errors
$ErrorActionPreference = "Continue"
# Log all output to a file
if (-not (Test-Path "C:\ludus")) {
    New-Item -ItemType Directory -Path "C:\ludus"
}
Start-Transcript -path C:\ludus\setup-log.txt -append

function Expand-ZIPFile($file, $destination) {
  $shell = new-object -com shell.application
  $zip = $shell.NameSpace($file)
  foreach ($item in $zip.items()) {
    $shell.Namespace($destination).copyhere($item)
  }
}

New-Item -Path "C:\" -Name "Updates" -ItemType Directory

Write-Output "$(Get-Date -Format G): Downloading .NET 4.5"
(New-Object Net.WebClient).DownloadFile("https://download.microsoft.com/download/B/A/4/BA4A7E71-2906-4B2D-A0E1-80CF16844F5F/dotNetFx45_Full_setup.exe", "C:\Updates\dotNetFx45_Full_setup.exe")
C:\Updates\dotNetFx45_Full_setup.exe /q /norestart

Write-Output "$(Get-Date -Format G): Downloading Windows Management Framework 5.1"
(New-Object Net.WebClient).DownloadFile("https://download.microsoft.com/download/6/F/5/6F5FF66C-6775-42B0-86C4-47D41F2DA187/Win7AndW2K8R2-KB3191566-x64.zip", "C:\Updates\Win7AndW2K8R2-KB3191566-x64.zip")

Write-Output "$(Get-Date -Format G): Installing Windows Management Framework 5.1"
Expand-ZipFile "C:\Updates\Win7AndW2K8R2-KB3191566-x64.zip" -destination "C:\Updates"

Write-Output "$(Get-Date -Format G): Extracting $update"
Start-Process -FilePath "wusa.exe" -ArgumentList "C:\Updates\Win7AndW2K8R2-KB3191566-x64.msu /extract:C:\Updates" -Wait

Write-Output "$(Get-Date -Format G): Installing $update"
Start-Process -FilePath "dism.exe" -ArgumentList "/online /add-package /PackagePath:C:\Updates\Windows6.1-KB2809215-x64.cab /quiet /norestart /LogPath:C:\Windows\Temp\KB2809215-x64.log" -Wait
Start-Process -FilePath "dism.exe" -ArgumentList "/online /add-package /PackagePath:C:\Updates\Windows6.1-KB2872035-x64.cab /quiet /norestart /LogPath:C:\Windows\Temp\KB2872035-x64.log" -Wait
Start-Process -FilePath "dism.exe" -ArgumentList "/online /add-package /PackagePath:C:\Updates\Windows6.1-KB2872047-x64.cab /quiet /norestart /LogPath:C:\Windows\Temp\KB2872047-x64.log" -Wait
Start-Process -FilePath "dism.exe" -ArgumentList "/online /add-package /PackagePath:C:\Updates\Windows6.1-KB3033929-x64.cab /quiet /norestart /LogPath:C:\Windows\Temp\KB3033929-x64.log" -Wait
Start-Process -FilePath "dism.exe" -ArgumentList "/online /add-package /PackagePath:C:\Updates\Windows6.1-KB3191566-x64.cab /quiet /norestart /LogPath:C:\Windows\Temp\KB3191566-x64.log" -Wait

# Remove-Item -LiteralPath "C:\Updates" -Force -Recurse

Write-Output "$(Get-Date -Format G): Finished installing Windows Management Framework 5.1. The VM will now reboot and continue the installation process."
