# Based on:
#   https://github.com/jeremybeaume/tools/blob/master/disable-defender.ps1
#   https://www.powershellgallery.com/packages/PoshInternals/1.0/Content/MoveFile.ps1
#   https://devblogs.microsoft.com/scripting/weekend-scripter-use-powershell-and-pinvoke-to-remove-stubborn-files/
#   https://github.com/Sycnex/Windows10Debloater/blob/master/Windows10Debloater.ps1
 
Add-Type @"
    using System;
    using System.Text;
    using System.Runtime.InteropServices;
 
    public class Posh
    {
        public enum MoveFileFlags
        {
            MOVEFILE_REPLACE_EXISTING           = 0x00000001,
            MOVEFILE_COPY_ALLOWED               = 0x00000002,
            MOVEFILE_DELAY_UNTIL_REBOOT         = 0x00000004,
            MOVEFILE_WRITE_THROUGH              = 0x00000008,
            MOVEFILE_CREATE_HARDLINK            = 0x00000010,
            MOVEFILE_FAIL_IF_NOT_TRACKABLE      = 0x00000020
        }
 
        [DllImport("kernel32.dll", SetLastError = true, CharSet = CharSet.Unicode)]
        static extern bool MoveFileEx(string lpExistingFileName, string lpNewFileName, MoveFileFlags dwFlags);
        public static bool MarkFileRename (string sourcefile, string destfile)
        {
            bool brc = false;
            brc = MoveFileEx(sourcefile, destfile, MoveFileFlags.MOVEFILE_DELAY_UNTIL_REBOOT);        
            return brc;
        }
    }
"@
 
$signature = @'
    [DllImport("Kernel32.dll")]
    public static extern uint GetLastError();
'@
Add-Type -MemberDefinition $signature -Name API -Namespace Win32
 
if(-Not $($(whoami) -eq "nt authority\system")) {
    $IsSystem = $false
 
    # Elevate to admin (needed when called after reboot)
    if (-Not ([Security.Principal.WindowsPrincipal] [Security.Principal.WindowsIdentity]::GetCurrent()).IsInRole([Security.Principal.WindowsBuiltInRole] 'Administrator')) {
        Write-Host "[i] Elevate to Administrator"
        $CommandLine = "-ExecutionPolicy Bypass `"" + $MyInvocation.MyCommand.Path + "`" " + $MyInvocation.UnboundArguments
        Start-Process -FilePath PowerShell.exe -Verb Runas -ArgumentList $CommandLine
        Exit
    }
 
    # Elevate to SYSTEM if psexec is available
    $psexecPath = "C:\Tools\Sysinternals\PsExec64.exe"
    $psexecDir = "C:\Tools\Sysinternals"
    $localPsexecPath = $(Get-Command PsExec -ErrorAction 'ignore').Source
    if($localPsexecPath) {
        Write-Host "[i] Elevate to SYSTEM"
        $CommandLine = " -i -s powershell.exe -ExecutionPolicy Bypass `"" + $MyInvocation.MyCommand.Path + "`" " + $MyInvocation.UnboundArguments
        Start-Process -WindowStyle Hidden -FilePath $localPsexecPath -ArgumentList $CommandLine
        exit
    } else {
        Write-Host "$('[{0:HH:mm}]' -f (Get-Date)) Downloading PsExec64.exe..."
        If(!(test-path $psexecDir)) {
            New-Item -ItemType Directory -Force -Path $psexecDir
        }
        Try { 
            (New-Object System.Net.WebClient).DownloadFile('https://live.sysinternals.com/PsExec64.exe', $psexecPath)
        } Catch { 
            Write-Host "HTTPS connection failed. Switching to HTTP :("
            (New-Object System.Net.WebClient).DownloadFile('http://live.sysinternals.com/PsExec64.exe', $psexecPath)
        }
        Write-Host "[i] Elevate to SYSTEM"
        $CommandLine = " -accepteula -i -s powershell.exe -ExecutionPolicy Bypass `"" + $MyInvocation.MyCommand.Path + "`" " + $MyInvocation.UnboundArguments
        Start-Process -WindowStyle Hidden -FilePath $psexecPath -ArgumentList $CommandLine
        exit
    }
 
} else {
    $IsSystem = $true
}
 
# Shhh
$ErrorActionPreference = "SilentlyContinue"

Write-Output "[+] whoami:"
whoami
 
Write-Output "[+] Stopping and disabling Diagnostics Tracking Service"
Stop-Service "DiagTrack"
Set-Service "DiagTrack" -StartupType Disabled
 
#Disables Windows Feedback Experience
Write-Output "[+] Disabling Windows Feedback Experience program"
$Advertising = "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\AdvertisingInfo"
If (Test-Path $Advertising) {
    Set-ItemProperty $Advertising Enabled -Value 0
}
         
#Stops Cortana from being used as part of your Windows Search Function
Write-Output "[+] Stopping Cortana from being used as part of your Windows Search Function"
$Search = "HKLM:\SOFTWARE\Policies\Microsoft\Windows\Windows Search"
If (Test-Path $Search) {
    Set-ItemProperty $Search AllowCortana -Value 0
}
 
#Disables Web Search in Start Menu
Write-Output "[+] Disabling Bing Search in Start Menu"
$WebSearch = "HKLM:\SOFTWARE\Policies\Microsoft\Windows\Windows Search"
Set-ItemProperty "HKCU:\SOFTWARE\Microsoft\Windows\CurrentVersion\Search" BingSearchEnabled -Value 0
If (!(Test-Path $WebSearch)) {
    New-Item $WebSearch | Out-Null
}
Set-ItemProperty $WebSearch DisableWebSearch -Value 1 | Out-Null
         
#Stops the Windows Feedback Experience from sending anonymous data
Write-Output "[+] Stopping the Windows Feedback Experience program"
$Period = "HKCU:\Software\Microsoft\Siuf\Rules"
If (!(Test-Path $Period)) {
    New-Item $Period
}
Set-ItemProperty $Period PeriodInNanoSeconds -Value 0
 
#Turns off Data Collection via the AllowTelemtry key by changing it to 0
Write-Output "[+] Turning off Data Collection"
$DataCollection1 = "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\DataCollection"
$DataCollection2 = "HKLM:\SOFTWARE\Policies\Microsoft\Windows\DataCollection"
$DataCollection3 = "HKLM:\SOFTWARE\Wow6432Node\Microsoft\Windows\CurrentVersion\Policies\DataCollection"   
If (Test-Path $DataCollection1) {
    Set-ItemProperty $DataCollection1  AllowTelemetry -Value 0
}
If (Test-Path $DataCollection2) {
    Set-ItemProperty $DataCollection2  AllowTelemetry -Value 0
}
If (Test-Path $DataCollection3) {
    Set-ItemProperty $DataCollection3  AllowTelemetry -Value 0
}
 
#Disabling Location Tracking
Write-Output "[+] Disabling Location Tracking"
$SensorState = "HKLM:\SOFTWARE\Microsoft\Windows NT\CurrentVersion\Sensor\Overrides\{BFA794E4-F964-4FDB-90F6-51056BFE4B44}"
$LocationConfig = "HKLM:\SYSTEM\CurrentControlSet\Services\lfsvc\Service\Configuration"
If (!(Test-Path $SensorState)) {
    New-Item $SensorState
}
Set-ItemProperty $SensorState SensorPermissionState -Value 0
If (!(Test-Path $LocationConfig)) {
    New-Item $LocationConfig
}
Set-ItemProperty $LocationConfig Status -Value 0
 
if($IsSystem) {
 
    # Configure the Defender registry to disable it (and the TamperProtection)
    # editing HKLM:\SOFTWARE\Microsoft\Windows Defender\ requires to be SYSTEM
    # Even that isn't enough?
 
    Write-Host "[+] Disabling functionalities with registry keys (SYSTEM privilege)"
 
    Write-Output "[+] Disabling Cloud-delivered protection"
    Set-ItemProperty -Path "HKLM:\SOFTWARE\Microsoft\Windows Defender\Real-Time Protection" -Name SpyNetReporting -Value 0
    Set-MpPreference -MAPSReporting Disable
 
    Write-Output "[+] Disabling Automatic Sample submission"
    Set-ItemProperty -Path "HKLM:\SOFTWARE\Microsoft\Windows Defender\Real-Time Protection" -Name SubmitSamplesConsent -Value 0
    Set-MpPreference -SubmitSamplesConsent Never
  
 
    # This doesn't work since diagtrack can only be changed by TrustedInstaller?
    Write-Output "[+] Setting the Diagnostics Tracking Service dll to be renamed on reboot"
    [Posh]::MarkFileRename("C:\Windows\System32\diagtrack.dll", "C:\Windows\System32\diagtrack.dll.bak")
    #[Win32.API]::GetLastError()
    # 5 == Access denied
    #(Get-ItemProperty -Path "HKLM:\SYSTEM\CurrentControlSet\Control\Session Manager" -Name "PendingFileRenameOperations").PendingFileRenameOperations
 
} else {
    Write-Host "[W] (Optional) Cannot rename diagtrack.dll or disable cloud-delivered protection and sample submission (not SYSTEM)"
}

# Clean up
sc.exe delete PSEXESVC
del C:\Windows\PSEXESVC.exe