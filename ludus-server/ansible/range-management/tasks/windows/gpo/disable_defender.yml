# Based on https://github.com/xbufu/ansible-role-ad_gpos/blob/main/tasks/disable_defender.yml
# MIT License
---
- name: Create GPO to disable Windows Defender
  ansible.windows.win_shell: |
    Import-Module GroupPolicy -Verbose:$false

    $GPOName = "Disable Windows Defender"
    $GPOExists = Get-GPO -Name $GPOName -ErrorAction SilentlyContinue

    if (! $GPOExists) {
      Write-Host "Creating GPO..."
      New-GPO -Name $GPOName | Out-Null

      $Domain = Get-ADDomain
      $Forest = $Domain.Forest
      $DN = $Domain.DistinguishedName
      $TargetOU = $DN

      Write-Host "Disabling Windows Defender..."

      $Params = @{
          Name = $GPOName;
          Key = 'HKLM\Software\Policies\Microsoft\Windows Defender';
      }

      try {
          Set-GPRegistryValue @Params -ValueName "DisableAntiSpyware" -Value 1 -Type DWORD | Out-Null
      } catch {
          Write-Error "Error while configuring Windows Defender policy!"
      }

      Write-Host "Disabling Real-Time Protection..."

      $Params = @{
          Name = $GPOName;
          Key = 'HKLM\Software\Policies\Microsoft\Windows Defender\Real-Time Protection';
      }

      try {
          Set-GPRegistryValue @Params -ValueName "DisableRealtimeMonitoring" -Value 1 -Type DWORD | Out-Null
      } catch {
          Write-Error "Error while configuring Real-Time Protection policy!"
      }

      Write-Host "Disabling the WinDefend Service..."

      $Params = @{
          Name = $GPOName;
          Key = 'HKLM\SYSTEM\CurrentControlSet\services\WinDefend';
      }

      try {
          Set-GPRegistryValue @Params -ValueName "Start" -Value 4 -Type DWORD | Out-Null
      } catch {
          Write-Error "Error while disabling the WinDefend Service!"
      }

      Write-Host "Disabling the WinDefend Service even harder..."

      $Params = @{
          Name = $GPOName;
          Key = 'HKLM\SYSTEM\CurrentControlSet\services\WinDefend';
      }

      try {
          Set-GPRegistryValue @Params -ValueName "ImagePath" -Value "C:\GOAWAYDEFENDER.txt" -Type ExpandString | Out-Null
      } catch {
          Write-Error "Error while disabling the WinDefend Service even harder!"
      }

      Write-Host "Disabling Routine Remediation..."

      $Params = @{
          Name = $GPOName;
          Key = 'HKLM\Software\Policies\Microsoft\Windows Defender';
      }

      try {
          Set-GPRegistryValue @Params -ValueName "DisableRoutinelyTakingAction" -Value 1 -Type DWORD | Out-Null
      } catch {
          Write-Error "Error while configuring Routine Remediation policy!"
      }

      Write-Host "Disabling Automatic Sample Submission..."

      $Params = @{
          Name = $GPOName;
          Key = 'HKLM\Software\Policies\Microsoft\Windows Defender\Spynet';
      }

      try {
          Set-GPRegistryValue @Params -ValueName "SpynetReporting" -Value 0 -Type DWORD | Out-Null
          Set-GPRegistryValue @Params -ValueName "SubmitSamplesConsent" -Value 2 -Type DWORD | Out-Null
      } catch {
          Write-Error "Error while configuring Automatic Sample Submission policy!"
      }

      Write-Host "Disabling SmartScreen on Edge..."

      $Params = @{
          Name = $GPOName;
          Key = 'HKLM\Software\Policies\Microsoft\Edge';
      }

      try {
          Set-GPRegistryValue @Params -ValueName "SmartScreenEnabled" -Value 0 -Type DWORD | Out-Null
          Set-GPRegistryValue @Params -ValueName "SmartScreenPuaEnabled" -Value 0 -Type DWORD | Out-Null
      } catch {
          Write-Error "Error while configuring Automatic Sample Submission policy!"
      }

      Write-Host "Configuring Security Filter..."

      Set-GPPermissions -Name $GPOName -PermissionLevel GpoApply -TargetName "Domain Computers" -TargetType Group | Out-Null
      Set-GPPermissions -Name $GPOName -PermissionLevel GpoApply -TargetName "Domain Users" -TargetType User | Out-Null

      Write-Host "Linking and enabling new GPO..."

      New-GPLink -Name $GPOName -Target $TargetOU -LinkEnabled Yes -Enforced Yes | Out-Null
    }
