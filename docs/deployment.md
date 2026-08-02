# Deployment: remote inference, and running CAS anywhere

CAS is local-first: the binary, the SQLite state (`~/.cas/`), and the
plugins (`~/.cas/plugins/`) all live on the machine where `cas` runs.
Inference is the part you can move. That gives three topologies:

1. **Everything local** — the README Quick Start. Ollama and CAS on one
   machine. Nothing more to configure.
2. **Thin client** — CAS on your laptop, Ollama on a GPU box, connected
   over Tailscale. The laptop needs no GPU and no models. This page is
   mostly about this setup.
3. **Remote CAS** — CAS itself runs on the remote box; you attach to the
   TUI over SSH. One shared session history across all your devices.

Cloud providers (Anthropic, Groq, OpenAI, OpenRouter) are a fourth option
that needs no deployment at all — just the API-key environment variables
from the README.

## Thin client: remote Ollama over Tailscale

Ollama speaks plain HTTP and has **no authentication**. Tailscale is what
makes exposing it sane: the API becomes reachable from your devices and
nothing else. Do not expose port 11434 to a LAN you don't fully trust,
and never to the internet.

### 1. On the GPU box: make Ollama listen beyond loopback

Ollama binds `127.0.0.1:11434` by default. Its own `OLLAMA_HOST` variable
controls the bind address (not to be confused with `OLLAMA_BASE_URL`,
which is what the CAS client reads). With the standard Linux install,
Ollama runs under systemd:

```bash
sudo systemctl edit ollama
```

```ini
[Service]
Environment="OLLAMA_HOST=0.0.0.0:11434"
```

```bash
sudo systemctl restart ollama
```

`0.0.0.0` is fine when the box's firewall keeps 11434 off untrusted
networks. To be strict, bind to the machine's Tailscale IP instead
(`tailscale ip -4` prints it):

```ini
[Service]
Environment="OLLAMA_HOST=100.x.y.z:11434"
```

Then the API is reachable from your tailnet and nowhere else — not even
the local LAN. Tailscale ACLs can narrow it further to specific devices
if you share the tailnet.

### 2. Pull models on the GPU box

CAS's Ollama defaults are `qwen3.5:9b` (document, list, chat) and
`qwen2.5-coder:7b` (code). Models live where Ollama runs, so pull them
there:

```bash
ollama pull qwen3.5:9b
ollama pull qwen2.5-coder:7b
```

### 3. On the laptop: verify, point CAS at it, run

With MagicDNS on, the box is reachable by hostname; otherwise use its
100.x address.

```bash
curl http://gpubox:11434/api/tags        # should list the pulled models

export OLLAMA_BASE_URL=http://gpubox:11434
cas
```

`cas --providers` confirms what the client is configured to use. Any
platform's binary works as the client — the laptop is just a TUI plus an
HTTP connection.

### 4. Optional: bigger models, per workspace type

A GPU box usually means headroom. `CAS_MODEL_{TYPE}` overrides the model
per workspace type — `DOCUMENT`, `LIST`, `CODE`, `CHAT`:

```bash
export CAS_MODEL_CODE=qwen2.5-coder:32b
export CAS_MODEL_CHAT=qwen3.5:32b
cas
```

Anything `ollama list` shows on the server is fair game.

## Remote CAS over SSH

The inverse arrangement: run `cas` on the remote box and attach to it.

```bash
ssh -t gpubox cas          # -t allocates the TTY the TUI needs
```

Or inside tmux on the box, so the session survives disconnects:

```bash
ssh gpubox
tmux new -s cas
cas
```

State follows the binary: with remote CAS, the SQLite history and plugins
are the box's, shared by every device that attaches. With the thin-client
setup, each machine keeps its own. Pick by whether you want one
continuous session or per-device ones.

## What travels where

With remote Ollama, prompts and workspace content leave your laptop —
but only to your own hardware, over an encrypted tailnet connection, and
nothing is retained anywhere you don't control. The cloud providers are
the opposite trade: no hardware to run, in exchange for sending content
to a third party.

## Troubleshooting

- **`connection refused`** — Ollama is still bound to loopback on the
  server. Revisit step 1; `ss -ltn | grep 11434` on the box shows the
  actual bind address.
- **Model errors on first message** — the model isn't pulled *on the
  server*. `ollama list` there, not on the laptop.
- **Hostname doesn't resolve** — MagicDNS is off or the device is logged
  out. `tailscale status` on the laptop; fall back to the 100.x IP.
- **Not sure what CAS is talking to** — `cas --providers` prints the
  active provider; `echo $OLLAMA_BASE_URL` confirms the endpoint.
