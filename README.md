# GPUless

GPUless is a self-hosted panel that runs image and voice models on the free
GPU hours of **your own** Kaggle account — 30 hours a week, at the time of
writing. The server it runs on needs no graphics card of its own: a mini PC or
a Raspberry Pi is enough, because all it does is serve the page and drive the
remote kernel. One Docker image, one SQLite file.

Nothing is routed through anybody else's account. Each installation brings its
own Kaggle credentials and spends its own quota.

## Contents

- [How it works](#how-it-works)
- [What Kaggle does and does not allow](#what-kaggle-does-and-does-not-allow)
- [Requirements](#requirements)
- [Installation](#installation)
- [Setup](#setup)
- [The runtime dataset](#the-runtime-dataset)
- [Budget and the kernel lifecycle](#budget-and-the-kernel-lifecycle)
- [Workflows](#workflows)
- [Configuration](#configuration)
- [Security](#security)
- [Building from source](#building-from-source)
- [Limitations](#limitations)
- [Roadmap](#roadmap)

## How it works

```
     your server (no GPU)                         Kaggle (free GPU)
 ┌───────────────────────────┐              ┌────────────────────────────┐
 │  browser → gpuless panel  │              │  runner script             │
 │             │             │              │   ├─ ComfyUI :8188         │
 │             │  kernels/push ────────────►│   ├─ edge     :8189        │
 │             │             │              │   └─ cloudflared ──┐       │
 │             └── HTTPS ────┼──────────────┼── gpu.example.com ◄┘       │
 └───────────────────────────┘              └────────────────────────────┘
```

1. The panel pushes a private notebook to your Kaggle account through the
   official API. Pushing a version is what runs it.
2. The notebook starts ComfyUI, puts a small authenticating edge in front of
   it, and dials out to your Cloudflare tunnel.
3. The panel reaches ComfyUI through that hostname, queues a workflow and
   downloads the result.
4. When nothing has been asked of it for a few minutes, the kernel stops
   itself and the weekly clock stops with it.

## What Kaggle does and does not allow

These constraints shaped the design, and it is worth knowing them before you
start:

- **Kaggle runs its own notebook image.** You cannot hand it a container. The
  ComfyUI runtime therefore ships as a *dataset* that gets mounted, not as an
  image that gets pulled — see [the runtime dataset](#the-runtime-dataset).
- **A kernel accepts no incoming connections.** It has to dial out, which is
  why a tunnel is required rather than optional.
- **There is no API to stop a running kernel.** The panel can only ask; the
  runner script watches its own idle time and exits. The panel's `Stop now`
  button posts to that script.
- **There is no API for the quota you have left.** Kaggle publishes the figure
  only in its web interface. GPUless counts the hours itself — it starts and
  stops every session, so its tally is its own. Hours you spend on kaggle.com
  directly are not in that number.

## Requirements

- A machine that can run one small container. An ARM Raspberry Pi 4 is plenty.
- A **Kaggle account**, verified for GPU use, and an API token.
- A **Cloudflare tunnel** with a public hostname. The free tier is enough, and
  it needs no open port on your side.
- One Kaggle dataset holding the ComfyUI runtime, plus a dataset per model.

## Installation

```sh
mkdir gpuless && cd gpuless
curl -O https://raw.githubusercontent.com/johanneshehl/GpuLess/main/docker-compose.yml
docker compose up -d
```

Then open `http://your-server:8080` and follow the wizard.

Put a reverse proxy in front of it if it should be reachable from outside your
network, and set `GPULESS_BEHIND_PROXY=1` so the panel trusts the forwarded
address and protocol.

## Setup

The wizard has four steps and can be resumed: whatever it already accepted is
kept if you close the tab.

1. **Administrator** — the account that signs in to the panel.
2. **Kaggle** — username and API token from
   *kaggle.com → Settings → API → Create New Token*. The panel verifies them
   before moving on.
3. **Tunnel** — the public hostname and the tunnel token from
   *one.dash.cloudflare.com → Networks → Tunnels*. Route the hostname to
   `http://localhost:8189`, which is the runner's edge, not ComfyUI itself.
4. **Models** — the dataset references. The runtime one is required; leave a
   model empty to hide its tab.

## The runtime dataset

Kaggle cannot pull your container, so ComfyUI and its dependencies are mounted
from a dataset on your account. [`docs/runtime-dataset.md`](docs/runtime-dataset.md)
walks through building one. In short it holds:

```
ComfyUI/            the checkout, with its custom nodes
site-packages/      anything the Kaggle image does not already have
bin/cloudflared     the tunnel client
```

Model weights live in their own datasets and are mounted into ComfyUI's model
folders, so a cold start costs seconds of mounting rather than minutes of
downloading.

## Budget and the kernel lifecycle

A Kaggle session spends the weekly allowance from the moment it is allocated,
whether or not anything is being generated. That makes idle time the single
biggest way to waste the budget, so the defaults are deliberately frugal:

| Setting | Default | Why |
|---|---|---|
| Stop when idle | 5 minutes | Kaggle's own cut-off is 20 minutes of inactivity. Stopping sooner keeps the difference. |
| Warm up on first visit | on | Hides the cold start behind the time it takes to read the page. |
| Keep alive while a tab is open | **off** | A tab left open overnight can cost most of a week. |
| Hard session limit | 9 hours | A backstop for a kernel that never goes idle. |
| Refuse new runs at | 98 % | Leaves a margin rather than running the account dry. |

Both sides enforce the idle stop: the runner script exits on its own, and the
panel checks as a backstop in case its timer is wedged. A cold start after an
auto-stop costs roughly 40 seconds.

The panel counts billed time from the moment it asks Kaggle for a session, not
from the moment ComfyUI answers — the cold start is billed too, so pretending
otherwise would understate the figure. A kernel that outlives a panel restart
is picked back up rather than left running unwatched.

## Workflows

Generation goes through ordinary ComfyUI graphs in API format. The built-in
ones live in `web/workflows/`, and each carries a small `_gpuless` block that
says how to fill it in:

```json
{
  "_gpuless": {
    "output_nodes": ["9"],
    "bind": { "prompt": ["6", "text"], "steps": ["3", "steps"] }
  },
  "6": { "class_type": "CLIPTextEncode", "inputs": { "text": "" } }
}
```

Drop a replacement `image.json` or `voice.json` into the directory named by
`GPULESS_WORKFLOWS` and the panel prefers it — no rebuild. This matters most
for voice: text to speech is not part of ComfyUI, so the default graph targets
an XTTS node pack, and node class names differ between packs. A broken
override fails at start-up with the file name, not silently at first use.

## Configuration

Flags, or the matching environment variables:

| Flag | Environment | Default | Meaning |
|---|---|---|---|
| `-addr` | `GPULESS_ADDR` | `:8080` | Listen address |
| `-data` | `GPULESS_DATA` | `./data` | Database and generated media |
| `-workflows` | `GPULESS_WORKFLOWS` | — | Directory of workflow overrides |
| `-behind-proxy` | `GPULESS_BEHIND_PROXY=1` | off | Trust `X-Forwarded-For` and `-Proto` |
| `-log` | `GPULESS_LOG` | `text` | `text` or `json` |

Everything else is in the panel's settings and lives in the database, so a new
container on the same volume comes back identical.

## Security

- Passwords are bcrypt at cost 12. Session cookies are random 256-bit tokens;
  only their SHA-256 is stored, so a copied database hands over no live
  sessions.
- Repeated failures lock an address out for 15 minutes.
- The tunnel hostname is public, so **every** request to the kernel carries a
  256-bit shared secret the panel generates and the runner checks. Without it
  the edge answers 401 and nothing reaches ComfyUI.
- The runner notebook contains the tunnel token and that secret, so it is
  always pushed private. Do not make it public.
- The panel sets a strict CSP, loads no third-party scripts, and makes no
  outbound request other than to Kaggle and your own tunnel.
- Setup endpoints close once setup finishes; afterwards they need a session.

## Checking the Kaggle API

The Kaggle client in this repository was written against the documented
endpoints and is tested against fakes. To turn that into evidence on your own
account:

```sh
go run . -check-kaggle          # or: gpuless -check-kaggle
```

It reads credentials from `KAGGLE_USERNAME`/`KAGGLE_KEY`, from
`~/.kaggle/kaggle.json`, or from a panel that is already set up, then
exercises the four calls the panel depends on — authenticate, push, status,
read the log — and prints what each one did. The notebook it pushes is
private, CPU-only and has no internet, so it costs nothing from the weekly GPU
allowance. Your token never appears in the output, so the report is safe to
paste anywhere.

Delete the `gpuless-apicheck` notebook afterwards if you like a tidy account.

## Building from source

```sh
git clone https://github.com/johanneshehl/GpuLess
cd GpuLess
go test ./...
CGO_ENABLED=0 go build -o gpuless .
./gpuless -data ./data
```

There is no build step for the front end and no CGO: the SQLite driver is pure
Go, so the result is one static binary that runs anywhere the toolchain can
cross-compile to.

## Limitations

Worth knowing before you rely on it:

- **The quota figure is an estimate.** It is the panel's own tally. It cannot
  see hours you spend on kaggle.com yourself, and it will drift from Kaggle's
  number if you do.
- **A cold start is about 40 seconds**, and there is no way around it: Kaggle
  has no standby that pauses the clock while a kernel stays warm.
- **The voice workflow depends on a node pack** that is not part of ComfyUI.
  If yours uses different class names, override the graph.
- **One kernel at a time.** Two people generating at once queue behind each
  other inside ComfyUI.
- **Kaggle may hand you a different accelerator** than the one you asked for.
  The panel shows what the kernel actually reports rather than what was
  requested.

## Roadmap

- Upscaling and image-to-image on the Image tab.
- Voice cloning from an uploaded sample rather than a file already in the
  dataset.
- Tailscale as an alternative to the Cloudflare tunnel.
- A second user role that can generate but not change settings.
- Prometheus metrics for kernel time and run counts.

## Acknowledgements

Uses [ComfyUI](https://github.com/comfyanonymous/ComfyUI),
[modernc.org/sqlite](https://gitlab.com/cznic/sqlite),
[golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto) and the
[Geist](https://vercel.com/font) typeface. The interface shares its visual
system with [Wicket](https://github.com/johanneshehl/Wicket).
