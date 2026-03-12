# Windows Wallpaper

This role sets the Windows wallpaper to a supplied files without using GPO

Supported platforms:

- Windows 11
- Windows 10
- Windows Server 2022
- Windows Server 2019
- Windows Server 2016
- Windows Server 2012R2

## Usage

Copy your images to 

`/opt/ludus/users/<username>/.ansible/roles/ludus_set_wallpaper/files`

or if installed globally at

`/opt/ludus/resources/global-roles/ludus_set_wallpaper/files`

The files must be named the values of `ludus_wallpaper_path` and `ludus_lockscreen_path`.

You can also set the values of `ludus_wallpaper_path` and `ludus_lockscreen_path` to URLs that will be downloaded during deploy.

## Role Variables

```yaml
ludus_primary_wallpaper_path: C:\Windows\Web\Wallpaper\Windows\img0.jpg
ludus_secondary_wallpaper_path: C:\Windows\Web\Wallpaper\Windows\img19.jpg
ludus_lockscreen_jpg_path: C:\Windows\Web\Screen\img100.jpg
ludus_wallpaper_path: files/wallpaper.jpg # Can be a URL to download during deploy
ludus_wallpaper_download_path: C:\wallpaper.jpg
ludus_lockscreen_path: files/lockscreen.jpg # Can be a URL to download during deploy
ludus_lockscreen_download_path: C:\lockscreen.jpg
ludus_tile_wallpaper: '0' # 0 = no tiling, 1 = tiled
ludus_wallpaper_style: '10' # 10 = centered, 6 = stretched, 23 = tiled, 62 = spanned
ludus_replace_login_screen: true
ludus_replace_all_size_wallpapers: true
ludus_resized_wallpapers_path: C:\Windows\Web\4K\Wallpaper\Windows\
ludus_remove_cached_wallpapers: true
ludus_remove_cached_lockscreen: true
ludus_lockscreen_system_path: C:\ProgramData\Microsoft\Windows\SystemData
```


## Example Ludus Range Config

```yaml
ludus:
  - vm_name: "{{ range_id }}-CLIENT-WIN10"
    hostname: "{{ range_id }}-CLIENT-WIN10"
    template: win10-21h2-x64-enterprise-template
    vlan: 10
    ip_last_octet: 20
    ram_gb: 8
    cpus: 4
    windows:
      sysprep: false
    domain:
      fqdn: ludus.domain
      role: member
    roles:
      - ludus_set_wallpaper
```
