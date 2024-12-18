#===============================================================================
#
#          FILE: install-client.ps1
# 
#         USAGE: iex ((New-Object System.Net.WebClient).DownloadString('https://ludus.cloud/install-client.ps1'))
#                irm https://ludus.cloud/install-client.ps1 | iex
# 
#   DESCRIPTION: Ludus CLI Installer Script.
#
#                This script installs the Ludus client into %LOCALAPPDATA%\Ludus\ludus.exe.
#
#       OPTIONS: none
#
#  REQUIREMENTS: powershell
#
#          BUGS: Please report.
#
#         NOTES: Homepage: https://ludus.cloud
#                  Issues: https://gitlab.com/badsectorlabs/ludus/-/issues
#
#===============================================================================

#-------------------------------------------------------------------------------
# FUNCTIONS
#-------------------------------------------------------------------------------

#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  Print-Banner
#   DESCRIPTION:  Prints a banner.
#    PARAMETERS:  none
#       RETURNS:  none
#-------------------------------------------------------------------------------
function Print-Banner {
    $banner = @"
====================================
 _      _   _  ____   _   _  ____  
| |    | | | ||  _ \ | | | |/ ___\ 
| |    | | | || | | || | | |\___ \ 
| |___ | |_| || |_| || |_| | ___) |
|____/  \___/ |____/  \___/  \___/ 

====================================
"@
    Write-Host $banner
}

#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  Get-Architecture
#   DESCRIPTION:  Detects the OS architecture and maps it to the appropriate value.
#    PARAMETERS:  none
#       RETURNS:  Architecture string (amd64, 386, arm64, unknown)
#-------------------------------------------------------------------------------
function Get-Architecture {
    $architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture
    $archFound = switch ($architecture) {
        "X64"  { return "amd64" }
        "X86"  { return "386" }
        "Arm64" { return "arm64" }
        Default { 
            $cpu = Get-WmiObject Win32_Processor        
            $architecture = switch ($cpu.Architecture) {
                0 { "386" }
                9 { "amd64" }
                12 { "arm64" }
                default { "unknown" }
            }
            return $architecture
        }
    }
}

#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  Fetch-ReleaseLinks
#   DESCRIPTION:  Fetches the release links from GitLab API.
#    PARAMETERS:  none
#       RETURNS:  JSON object containing the release links.
#-------------------------------------------------------------------------------
function Fetch-ReleaseLinks {
    $releaseLinks = (Invoke-WebRequest -Uri "https://gitlab.com/api/v4/projects/54052321/releases/permalink/latest/assets/links" -UseBasicParsing).Content | ConvertFrom-Json
    return $releaseLinks
}

#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  Get-Version
#   DESCRIPTION:  Extracts the version number dynamically from the asset release link.
#                 Assumes all release links possess version numbers.
#    PARAMETERS:  $releaseLinks = JSON object containing the release links.
#       RETURNS:  Version string (e.g., "1.5.4")
#-------------------------------------------------------------------------------
function Get-Version {
    param (
        [array]$releaseLinks
    )
    $firstAsset = $releaseLinks[0].name
    if ($firstAsset -match '\d+\.\d+\.\d+') {
        return $matches[0]
    }
    else {
        Write-Error "[+] Error: Unable to identify the current version from the asset release link. Did something change with versioning?"
        exit
    }
}

#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  Download-File
#   DESCRIPTION:  Downloads a file to a specific location.
#    PARAMETERS:  $url = URL of the file to download.
#                 $destination = Destination path.
#                 $fileName = Name of the file to download.
#       RETURNS:  none
#-------------------------------------------------------------------------------
function Download-File {
    param (
        [string]$url,
        [string]$destination,
        [string]$fileName
    )
    Invoke-WebRequest -Uri $url -OutFile $destination
    Write-Output "[+] Downloaded $fileName to $destination."
}

#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  Get-Checksum
#   DESCRIPTION:  Retrieves the checksum from the checksum file based on architecture.
#    PARAMETERS:  $checksumFileContent = Content of the checksum file.
#                 $arch = Architecture to filter by.
#       RETURNS:  Expected checksum string
#-------------------------------------------------------------------------------

function Get-Checksum {
    param (
        [array]$checksumFileContent,
        [string]$arch
    )
    return ($checksumFileContent | Where-Object { $_ -match "windows-$arch" } | ForEach-Object { $_.Split(" ")[0] })
}

#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  Verify-Checksum
#   DESCRIPTION:  Verifies the checksum of a downloaded file.
#    PARAMETERS:  $file = Path to the file.
#                 $expectedChecksum = Expected checksum value.
#       RETURNS:  Boolean (True if checksum matches, False otherwise)
#-------------------------------------------------------------------------------
function Verify-Checksum {
    param (
        [string]$file,
        [string]$expectedChecksum
    )
    $downloadedFileHash = Get-FileHash -Algorithm SHA256 -Path $file
    return ($downloadedFileHash.Hash -eq $expectedChecksum)
}

#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  Add-LudusToPath
#   DESCRIPTION:  Adds the Ludus directory to the user's PATH environment variable.
#    PARAMETERS:  none
#       RETURNS:  none
#-------------------------------------------------------------------------------
function Add-LudusToPath {
    $ludusPath = "$env:LOCALAPPDATA\Ludus"

    $currentPath = [System.Environment]::GetEnvironmentVariable("Path", "User")

    if ($currentPath -notmatch [regex]::Escape($ludusPath)) {
        $newPath = "$currentPath;$ludusPath"

        [System.Environment]::SetEnvironmentVariable("Path", $newPath, "User")

        Write-Output "[+] Ludus binary directory has been added to the user PATH: $ludusPath"
    } else {
        Write-Output "[+] Ludus binary directory appears to already be in the user PATH. This is okay if you're upgrading."
    }
}

#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  Setup-AutoCompletion
#   DESCRIPTION:  Sets up PowerShell autocompletion for Ludus CLI, but only if it isn't already configured.
#    PARAMETERS:  $binaryPath = The path to the Ludus binary.
#       RETURNS:  none
#-------------------------------------------------------------------------------
function Setup-AutoCompletion {
    param (
        [string]$binaryPath
    )

    if (-not (Test-Path -Path $binaryPath)) {
        Write-Error "[+] Error: Ludus binary not found at $binaryPath. Unable to set up PowerShell completion."
        return
    }

    # Check if script execution is allowed
    $executionPolicy = Get-ExecutionPolicy
    if ($executionPolicy -eq "Restricted" -or $executionPolicy -eq "AllSigned") {
        Write-Warning "[!] PowerShell script execution is currently restricted or set to AllSigned. This may prevent autocompletion from working properly."
        Write-Warning "[+] To enable script execution, you may need to run the following command as an administrator:"
        Write-Warning "    Set-ExecutionPolicy Unrestricted"
        Write-Warning "[+] After changing the execution policy, please run this installer again."
        return
    } else {
        Write-Output "[+] PowerShell script execution is allowed. Proceeding with autocompletion setup."
    }


    if (-not (Test-Path -Path $PROFILE)) {
        Write-Output "[+] Profile file not found. Creating a new profile at $PROFILE."
        New-Item -ItemType File -Path $PROFILE -Force | Out-Null
    }

    $profileContent = Get-Content -Path $PROFILE -Raw
    if ($profileContent -match "ludus") {
        Write-Output "[+] Ludus autocompletion is already configured in your profile."
        Write-Output "[+] Ludus CLI Installation complete!"
    } else {
        "$binaryPath completion powershell >> $PROFILE | Select-Object CurrentUserCurrentHost" | Out-File -Append -FilePath $PROFILE
        Write-Output "[+] PowerShell autocompletion script added to your profile."
        Write-Output "[+] Ludus CLI Installation complete!"
    }
}

#---  FUNCTION  ----------------------------------------------------------------
#          NAME:  Install-Ludus
#   DESCRIPTION:  Handles downloading and verifying Ludus binary.
#    PARAMETERS:  none
#       RETURNS:  none
#-------------------------------------------------------------------------------
function Install-Ludus {
    $arch = Get-Architecture
    Write-Output "[+] Identified $arch as the current architecture."

    $releaseLinks = Fetch-ReleaseLinks

    $version = Get-Version -releaseLinks $releaseLinks
    Write-Output "[+] Identified $version as the latest release."

    $downloadLink = $releaseLinks | Where-Object { $_.name -match "windows-$arch" }
    
    if ($downloadLink) {
        Write-Output "[+] Found download link for current binary: $($downloadLink.name)"

        $destinationPath = "$env:LOCALAPPDATA\Ludus\ludus.exe"
        
        if (-not (Test-Path "$env:LOCALAPPDATA\Ludus")) {
            New-Item -ItemType Directory -Path "$env:LOCALAPPDATA\Ludus" | Out-Null
        }

        Download-File -url $downloadLink.direct_asset_url -destination $destinationPath -fileName $($downloadLink.name)

        $checksumLink = $releaseLinks | Where-Object { $_.name -match "checksums.txt" -and $_.name -match "$version" }

        if ($checksumLink) {
            Write-Output "[+] Found download link for checksum file: $($checksumLink.name)"

            $checksumPath = "$env:LOCALAPPDATA\Ludus\ludus_$version`_checksums.txt"
            Download-File -url $checksumLink.direct_asset_url -destination $checksumPath -fileName $($checksumLink.name)

            $checksumFileContent = Get-Content -Path $checksumPath

            $expectedChecksum = Get-Checksum -checksumFileContent $checksumFileContent -arch $arch

            if (Verify-Checksum -file $destinationPath -expectedChecksum $expectedChecksum) {
                Write-Output "[+] Checksums match!"
                Remove-Item $checksumPath
                Write-Output "[+] Checksum file cleaned up."

                if (-not ($env:Path -split ';' -contains "$env:LOCALAPPDATA\Ludus")) {
                    $addToPath = Read-Host "[?] Ludus is not in your PATH. Do you want to add it? (Y/N)"
                    if ($addToPath -eq 'Y' -or $addToPath -eq 'y') {
                        Add-LudusToPath
                        Write-Output "[+] Ludus added to PATH."
                    } else {
                        Write-Output "[+] Ludus not added to PATH. You can add it manually later if needed."
                    }
                } else {
                    Write-Output "[+] Ludus is already in your PATH."
                }

                
                if (Test-Path -Path $PROFILE) {
                    $profileContent = Get-Content -Path $PROFILE -Raw
                    if (-not ($profileContent -match "ludus")) {
                        $setupAutoCompletion = Read-Host "[?] Do you want to set up auto-completion for Ludus? (Y/N)"
                        if ($setupAutoCompletion -eq 'Y' -or $setupAutoCompletion -eq 'y') {
                            Setup-AutoCompletion -binaryPath $destinationPath
                            Write-Output "[+] Auto-completion for Ludus has been set up."
                        } else {
                            Write-Output "[+] Auto-completion for Ludus was not set up. You can set it up manually later if needed."
                        }
                    } else {
                        Write-Output "[+] Auto-completion for Ludus is already set up."
                    }
                } else {
                    Write-Output "[+] Profile file not found."
                    $setupAutoCompletion = Read-Host "[?] Do you want to set up auto-completion for Ludus? (Y/N)"
                    if ($setupAutoCompletion -eq 'Y' -or $setupAutoCompletion -eq 'y') {
                        Setup-AutoCompletion -binaryPath $destinationPath
                        Write-Output "[+] Auto-completion for Ludus has been set up."
                    } else {
                        Write-Output "[+] Auto-completion for Ludus was not set up. You can set it up manually later if needed."
                    }
                }

            } else {
                Write-Output "[+] Error: Checksum verification failed. The downloaded file may be corrupted or tampered with."
                Remove-Item $checksumPath, $destinationPath
                Write-Output "[+] Both checksum and binary files have been deleted due to failed verification."
            }
        } else {
            Write-Output "[+] Error: Checksum file not found for version: $version"
        }
    } else {
        Write-Output "[+] Error: No matching download found for architecture: $arch"
    }
}

#-------------------------------------------------------------------------------
# MAIN SCRIPT EXECUTION
#-------------------------------------------------------------------------------

# Print the banner
Print-Banner

# Install Ludus
Install-Ludus