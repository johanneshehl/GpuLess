# Design canvas sources

Artboards for the GPUless panel, authored as Design Component files and
published as one pan/zoom canvas.

| File | Screen |
|---|---|
| `Login.dc.html` | Sign in |
| `Setup.dc.html` | Setup wizard, step 2 — Kaggle credentials |
| `Tunnel.dc.html` | Setup wizard, step 3 — Cloudflare tunnel |
| `Main.dc.html` | Image tab (kernel warm) |
| `Voice.dc.html` | Voice tab (kernel cold start after auto-stop) |
| `Settings.dc.html` | Settings — Kaggle, GPU budget, kernel, models, language |
| `canvas.json` | Layout, artboard titles, sticky notes |

The visual system is lifted from [Wicket](https://github.com/johanneshehl/Wicket)
(`web/static/wicket.css`): `#000` / `#0a0a0a` surfaces, 1px `#1f1f1f` lines,
Geist and Geist Mono, 6px controls / 10px panels / 12px dialogs, `#50e3c2`
accent.

Copy is English; German and Spanish are offered in the UI.

`gpuless-webpanel.html` is the assembled canvas and is **not** tracked — it is
regenerated from the files above, so edit the artboards and re-seed rather than
editing it.
