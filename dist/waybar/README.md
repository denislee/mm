# Waybar widget for Claude Code usage

`cc-waybar` prints one line of Waybar JSON showing session + weekly usage bars.
`make install-widget` installs the binary to `~/.local/bin/cc-waybar`; the two
snippets below are the Waybar config you add by hand (Waybar's own config isn't
touched by the Makefile).

## 1. Module — add to `~/.config/waybar/config`

Add `"custom/claude"` to a `modules-*` array, then define the module:

```jsonc
"custom/claude": {
    "exec": "~/.local/bin/cc-waybar -width 16 -max-age 11m",
    "return-type": "json",
    "interval": 300,
    "tooltip": true,
    "on-click": "mm",
    "on-click-right": "true"
}
```

- `-width 16` — bar width in cells (per window). Bigger = wider bars.
- `-max-age 11m` — reuse cached data newer than this instead of fetching. With
  the systemd poller running (`make install-service`) the bar becomes a pure
  reader and only self-fetches if the poller stops for >11 min.
- `on-click` opens the `mm` GUI (install it with `make install`).

## 2. Style — add to `~/.config/waybar/style.css`

```css
#custom-claude {
  margin: 2px;
  padding-left: 6px;
  padding-right: 6px;
  background-color: rgba(0,0,0,0.3);
  font-weight: bold;
}
#custom-claude.ok       { color: #a3be8c; }
#custom-claude.warn     { color: #ebcb8b; }
#custom-claude.over     { color: #d08770; }
#custom-claude.error    { color: #6c7086; }
#custom-claude.critical {
  background-color: #bf616a;
  color: #ffffff;
  animation-name: blink;
  animation-duration: 1s;
  animation-timing-function: linear;
  animation-iteration-count: infinite;
  animation-direction: alternate;
}
```

## 3. Reload

```bash
killall -SIGUSR2 waybar
```

## Single-poller service (optional but recommended)

`make install-service` installs a `systemd --user` timer that refreshes the
shared cache every 5 minutes, so Waybar and the `mm` GUI never hit the usage
endpoint's rate limit. `make uninstall-service` removes it. See the units in
`dist/systemd/`.
