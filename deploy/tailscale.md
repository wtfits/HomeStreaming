# Remote access with Tailscale

## Why Tailscale

Your internet connection (Turksat cable, 100/10 Mbps) is behind **CGNAT**. The router's WAN address is `100.109.73.201`, a shared carrier address. Nobody on the internet can open a connection to your PC, so port forwarding on the router cannot work, and UPnP would only reach the carrier's NAT.

Tailscale creates an encrypted WireGuard network between your devices. It punches through CGNAT, needs no router changes, and exposes nothing to the public internet. The PC and the iPhone already have it installed.

**Rejected options:**
- **Cloudflare Tunnel**: the free plan's terms prohibit video streaming.
- **Port forwarding / built-in HTTPS certificates**: impossible behind CGNAT.

## Checklist

1. **PC: keep Tailscale running when nobody is logged in.** In the Tailscale tray menu: Settings → enable **Run unattended**.
2. **Disable key expiry for the PC**, so remote access doesn't stop after 180 days. In the [admin console](https://login.tailscale.com/admin/machines): your PC → ⋯ → **Disable key expiry**.
3. **Find the PC's Tailscale name.** Run `tailscale status` on the PC: the first line shows the machine name (for example `desktop-pc`) and its `100.x.y.z` address. HomeStreaming also lists the address in Admin → Status → Tailscale.
4. **Firewall**: the HomeStreaming rule must cover **all profiles**, because Windows treats the Tailscale adapter as Public. `service install` creates it that way; the command is also in [windows.md](windows.md).
5. **iPhone**: turn Tailscale on (or enable *VPN On Demand* in the Tailscale app so it connects automatically). In Safari open `http://desktop-pc:8420` (MagicDNS name) or `http://100.x.y.z:8420`, sign in, then Share → **Add to Home Screen**. Name it e.g. "HomeStreaming (away)". The home-network bookmark `http://192.168.0.5:8420` only works at home.
6. **LG TV**: it stays home-only. It cannot run Tailscale and doesn't need it.

## Quality away from home

Your upload is **10 Mbps**, and when Tailscale can't make a direct connection it relays traffic through its DERP servers, which have limited throughput. So remote streams are capped at **6 Mbps** by default (Admin → Server settings → *Remote bitrate cap*):

- Files above the cap are transcoded on the RTX 4090 to about 5.2 Mbps 1080p video plus 192 kbps AAC stereo.
- HDR sources go to the iPhone as HEVC 10-bit HDR when NVENC is available; otherwise they're tone-mapped to SDR H.264.
- Files under the cap play directly or remuxed, with no quality loss.

The player's Quality menu shows the applied cap, e.g. "Auto (6 Mbps, remote)". If playback stutters on mobile data, choose 4 or 2 Mbps there.

Tailscale clients (`100.64.0.0/10`) count as **remote**. If you also use Tailscale at home, set *Treat Tailscale clients as LAN* in Admin → Server settings, but only if your home Wi-Fi is the usual path.

## Optional: HTTPS with a real certificate

This is not required for playback; it gives a padlock and enables the PWA service worker.

1. In the Tailscale admin console enable **MagicDNS** and **HTTPS Certificates**.
2. On the PC, run as Administrator:
   ```powershell
   tailscale serve --bg http://127.0.0.1:8420
   ```
   You can now reach `https://desktop-pc.<your-tailnet>.ts.net`, with Tailscale managing the Let's Encrypt certificate.
3. So HomeStreaming sees the real client address behind `tailscale serve`, add this to `C:\ProgramData\HomeStreaming\config.toml`:
   ```toml
   [http]
   trusted_proxies = ["127.0.0.1"]
   ```
   Then restart the service.

## Later alternative: a VPS relay (not built)

For devices that cannot run Tailscale (a friend's TV), a small VPS with a public IPv4 can relay traffic:

- The PC keeps an outbound WireGuard tunnel to the VPS, so CGNAT doesn't matter.
- Caddy on the VPS terminates `https://media.example.com` with Let's Encrypt and forwards over the tunnel.
- Set HomeStreaming's `trusted_proxies` to the VPS tunnel IP.

Bandwidth is still limited by your 10 Mbps upload.
