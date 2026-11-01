# Running HomeStreaming on Windows

This is the setup for the current server PC: Windows 11, RTX 4090, media in `F:\torrent`, PC on wired Ethernet at `192.168.0.13`, LG TV at `192.168.0.10`, iPhone at `192.168.0.16`, router TEKNOTEL Infinity 401.

## 1. Get the program

1. Open <https://github.com/wtfits/HomeStreaming/releases/latest> and download **homestreaming-windows-amd64.zip** (no GitHub sign-in needed). Every push to `main` that passes CI is published there as a new version (`v0.1.N`).
2. Unzip it to `C:\Program Files\HomeStreaming\`. You should have `C:\Program Files\HomeStreaming\homestreaming.exe`.

This is the only manual download: after that, HomeStreaming updates itself (see [Updating](#updating)).

## 2. ffmpeg

ffmpeg is already installed via `winget install Gyan.FFmpeg`. HomeStreaming finds the winget copy automatically, even when it runs as a Windows service under a different account: it searches every user profile's WinGet folder. To check what it found, open PowerShell:

```powershell
& 'C:\Program Files\HomeStreaming\homestreaming.exe' doctor
```

Expected output: the `ffmpeg:` line shows the WinGet path. `hwaccel: configured=auto active=nvenc`. The self-tests list `encode_h264 ok`, `encode_hevc_main10 ok`, `decode_h264 ok` and `decode_hevc ok`. If ffmpeg is reported missing, open a **new** terminal after installing ffmpeg (so it can find the updated PATH), or set `path` under `[ffmpeg]` in `C:\ProgramData\HomeStreaming\config.toml`.

## 3. First start (in a terminal)

```powershell
& 'C:\Program Files\HomeStreaming\homestreaming.exe'
```

- Windows Defender Firewall asks whether to allow access. Tick **both Private and Public**. Public is needed because the Tailscale network adapter counts as a Public network.
- Open <http://localhost:8420>. The setup wizard opens:
  1. **ffmpeg**: the step should show green.
  2. **Account**: choose your username and password.
  3. **Media folder**: `F:\torrent` is pre-filled. Keep the type **Movies & shows (mixed)**, then click **Scan**.
  4. Wait for the scan to finish, then go to Home.

Scanning reads every file with ffprobe and extracts thumbnails. Your library of about 100 videos takes a few minutes. Items show a "processing" badge until they're ready. The game files, zips, `.!qB`/`.part` downloads, `Sample` folders and the DVD (`VIDEO_TS`) folder are skipped on purpose.

Stop the server with `Ctrl+C` once you've confirmed it works, then install it as a service.

## 4. Run as a Windows service (starts with Windows)

Open **PowerShell as Administrator**:

```powershell
& 'C:\Program Files\HomeStreaming\homestreaming.exe' service install
```

```powershell
& 'C:\Program Files\HomeStreaming\homestreaming.exe' service start
```

- The service runs as LocalSystem and uses the same data folder as the console run (`C:\ProgramData\HomeStreaming`), so your account, library and watch progress carry over.
- `service install` also adds the firewall rule (`TCP 8420`, all profiles). If that fails, run:
  ```powershell
  netsh advfirewall firewall add rule name="HomeStreaming" dir=in action=allow protocol=TCP localport=8420 profile=any
  ```
- To check, stop or remove the service, run `homestreaming.exe service status`, `service stop` or `service uninstall`.
- Media on internal drives such as `F:` works under LocalSystem. For a network share, install the service with `--user .\YourUser --password ...` instead.

## 5. Keep the PC reachable

- **Sleep**: Settings → System → Power → *Screen and sleep* → set "When plugged in, put my device to sleep after" to **Never**. A sleeping PC cannot stream.
- **Fixed IP address (recommended)**: the router hands out `192.168.0.10`–`254` and the PC currently has `.13`. To make bookmarks on the TV and iPhone survive lease changes, give the PC `192.168.0.5`, which is outside the router's range so it can never collide:
  1. Settings → Network & internet → Ethernet → **IP assignment** → Edit → **Manual** → IPv4 on.
  2. IP address `192.168.0.5`, subnet mask `255.255.255.0`, gateway `192.168.0.1`, preferred DNS `192.168.0.1` (alternate `1.1.1.1`). Save.

  If you skip this, use `192.168.0.13` everywhere below. It usually stays the same, but nothing guarantees it.
- **Network profile**: your Ethernet is already **Private**, which is correct for the home LAN.

## 6. Watch on your devices (at home)

| Device | How |
|---|---|
| This PC | <http://localhost:8420> in Chrome/Edge. HEVC/HDR files play directly if Edge/Chrome reports hardware HEVC support; otherwise the RTX 4090 transcodes them with NVENC. |
| LG TV (OLED65A16LA) | Open the **Web Browser** app → `http://192.168.0.5:8420`. TV mode switches on automatically: arrows move, OK selects, Back returns. Sign in with the on-screen keyboard once, then add the page to the TV's bookmarks/home. 4K HEVC HDR plays without re-encoding. DTS audio is converted on the fly (the A1 can't decode DTS). |
| iPhone 14 Pro | Safari → `http://192.168.0.5:8420` → sign in → Share → **Add to Home Screen**. The app opens full-screen like a native app. MKV files are repackaged on the fly (no quality loss). |

The player's **Quality** menu shows why a file is being converted, for example "Transcoding · nvenc H264 1080p — Why: bitrate above limit".

## 7. Away from home

See [tailscale.md](tailscale.md). Your ISP uses CGNAT, so port forwarding cannot work; Tailscale is the way in.

## Where things live

| What | Where |
|---|---|
| Config | `C:\ProgramData\HomeStreaming\config.toml` (ports, ffmpeg path, transcode folder, hwaccel) |
| Database (library, users, watch progress) | `C:\ProgramData\HomeStreaming\homestreaming.db` |
| Logs | `C:\ProgramData\HomeStreaming\logs\homestreaming.log` |
| Temporary transcode segments | `C:\ProgramData\HomeStreaming\transcode` (wiped at start). Move it with `dir = 'D:\hs-transcode'` under `[transcode]`. |

Runtime settings (remote bitrate cap, max simultaneous transcodes, scan interval and so on) are in the web UI under **Admin → Server settings**.

## Updating

HomeStreaming checks GitHub for a new release every 6 hours. When one is out, the **Admin** link shows a dot and **Admin → Updates** shows what changed with an **Install** button. Installing:

1. downloads `homestreaming-windows-amd64.zip` from the release and checks it against the release's `SHA256SUMS`;
2. runs the new `homestreaming.exe version` to make sure it starts on this PC;
3. renames the current program to `homestreaming.old.exe` and puts the new one in its place;
4. restarts the service. If the new version does not answer within 90 seconds, the old one is put back and restarted automatically, and Admin → Updates says why.

Playback stops for about 30 seconds during the restart; the page reloads by itself when the new version is up. The TV and iPhone need nothing: they load HomeStreaming from the PC, and an open page reloads into the new version on its own: straight away when you are browsing, after you leave the player when something is playing, and after you finish typing in a search or password box. A log of each restart is in `C:\ProgramData\HomeStreaming\logs\update.log`.

One-click install needs the Windows service (step 4), because only the service can write to `C:\Program Files`. If you run HomeStreaming in a terminal instead, Admin → Updates says so; install from an **Administrator** PowerShell and restart it yourself:

```powershell
& 'C:\Program Files\HomeStreaming\homestreaming.exe' update
```

`update --check` only reports whether a new version exists. The same `update` command, run as Administrator while the service is running, also restarts the service and rolls back on failure.

To stop the automatic checks, set `check = false` under `[update]` in `config.toml`. To go back to the previous version by hand: `service stop`, rename `homestreaming.old.exe` to `homestreaming.exe`, `service start`.

## Troubleshooting

- **The TV or iPhone can't connect**: check that `http://192.168.0.5:8420` opens on the PC itself, then run the firewall command from step 4. Run `homestreaming.exe doctor` to see the listen addresses.
- **"ffmpeg is missing"** in the UI: go to Admin → Status, enter the full path to `ffmpeg.exe`, then click *Check again*.
- **A file stays "processing"**: Admin → Problems shows the ffprobe error. Files that are still downloading are picked up automatically once qBittorrent finishes (the folder watcher waits 30 s after the last write).
- **No NVENC**: update the NVIDIA driver, then restart the service. HomeStreaming re-checks hardware every 10 minutes and falls back to software encoding (libx264 on the Ryzen 9) in the meantime.
