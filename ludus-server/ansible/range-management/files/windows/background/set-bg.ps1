# Hide the window
$t = '[DllImport("user32.dll")] public static extern bool ShowWindow(int handle, int state);'
add-type -name win -member $t -namespace native
[native.win]::ShowWindow(([System.Diagnostics.Process]::GetCurrentProcess() | Get-Process).MainWindowHandle, 0)

gwmi win32_quickfixengineering | sort InstalledOn -desc | Select -first 1 | foreach {$_.InstalledOn.toString("yyy-MM-dd")} | Out-File -FilePath C:\ludus\background\lastupdate.txt

function Test-Port
{
    param ( [string]$Computer = '.', [int]$Port = 3389, [int]$Millisecond = 300 )
    try {
        $ip = [System.Net.Dns]::GetHostAddresses($Computer) |
            select-object IPAddressToString -expandproperty  IPAddressToString
        if($ip.GetType().Name -eq "Object[]")
        {
            #If we have several ip's for that address, let's take first one
            $ip = $ip[0]
        }
    } catch {
        Write-Host "Possibly $Computer is wrong hostname or IP"
        return $False
    }

    #  Initialize object
    $Test = New-Object -TypeName Net.Sockets.TcpClient

    #  Attempt connection, 300 millisecond timeout, returns boolean
    ( $Test.BeginConnect( $ip, $Port, $Null, $Null ) ).AsyncWaitHandle.WaitOne( $Millisecond )

    # Cleanup
    $Test.Close()
}

while (1) {
    write-host "Waiting"
    Start-Sleep -seconds 30
    $current_color = "unset"

    if (Test-Connection -Cn 8.8.8.8 -Quiet -Count 1) {
        if ($current_color -ne "red") {
            C:\ludus\background\bginfo.exe /accepteula "C:\ludus\background\red.bgi" /silent /timer:0
            $current_color = "red"
        }
        continue
    }
    write-host "8.8.8.8 failed"
    if (Test-Port -Computer captive.apple.com -Port 80) {
        write-host "captive.apple.com worked"
        if ($current_color -ne "red") {
            C:\ludus\background\bginfo.exe /accepteula "C:\ludus\background\red.bgi" /silent /timer:0
            $current_color = "red"
        }
        continue
    }
    write-host "captive.apple.com failed"
    if (Test-Port -Computer google.com -Port 443) {
        if ($current_color -ne "red") {
            C:\ludus\background\bginfo.exe /accepteula "C:\ludus\background\red.bgi" /silent /timer:0
            $current_color = "red"
        }
        continue
    }
    write-host "google.com failed"
    if ($current_color -ne "green") {
        C:\ludus\background\bginfo.exe /accepteula "C:\ludus\background\green.bgi" /silent /timer:0
        $current_color = "green"
    }
}