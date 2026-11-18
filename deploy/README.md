# Deployment (Linux)

Release archives (`homecore-<version>-linux-{amd64,arm64}.tar.gz`) are built by
`.github/workflows/build.yml`. Each one holds the binary, `migrations/`, config example, systemd units,
polkit rule and a `README.md` with install/upgrade steps for humans and AI agents. That README is generated
from [package/README.md.tmpl](package/README.md.tmpl) by `scripts/package-backend.sh` (`make package` locally);
**keep the template in sync with the commands below**.

Manual install from a source build:

```bash
# Controller
sudo useradd --system --no-create-home --shell /usr/sbin/nologin homecore
sudo install -m 0755 build/homecore-linux-arm64 /usr/local/bin/homecore
sudo install -d -m 0750 /etc/homecore
sudo install -m 0640 -g homecore configs/homecore.example.yaml /etc/homecore/homecore.yaml
sudo install -m 0644 deploy/systemd/homecore-controller.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now homecore-controller

# Agent, recommended: no config file. In the web UI (device > Remote Shutdown) copy the command for this OS,
# then run the binary with nothing else and paste it: it writes homecore-agent.service and the polkit rule
# and prints the install steps (ADR-019).
#   ./homecore --mode agent
# Agent with a config file (the id is created on first start):
sudo useradd --system --no-create-home --shell /usr/sbin/nologin homecore-agent
sudo install -m 0755 build/homecore-linux-amd64 /usr/local/bin/homecore
sudo install -m 0640 -g homecore-agent agent.yaml /etc/homecore/agent.yaml   # mode: agent + trusted_controllers
sudo install -m 0644 deploy/polkit/50-homecore-agent.rules /etc/polkit-1/rules.d/
sudo install -m 0644 deploy/systemd/homecore-agent.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now homecore-agent
journalctl -u homecore-agent | grep agent_id     # bind this id to the device on the Controller

# First account on the Controller (no default account exists). Run it as the service user so the
# database file keeps its owner; works while the service runs. Prompts for the password twice
# (min. 12 characters); HOMECORE_PASSWORD or stdin also work for scripts.
sudo -u homecore homecore user add --config /etc/homecore/homecore.yaml alice
sudo -u homecore homecore user passwd --config /etc/homecore/homecore.yaml alice   # later: also signs the user out everywhere

# Controller signing key: created on first start (or by this command). Put the printed public key
# into `agent.trusted_controllers` of every agent that this Controller may shut down.
sudo -u homecore homecore key show --config /etc/homecore/homecore.yaml
# The same public key is at GET /api/v1/system/controller-key (any signed-in user).
```

# Windows Agent (shutdown/reboot implemented, not yet run on a real Windows host)
# Recommended: in the web UI (device > Remote Shutdown > Windows) copy the command, then in PowerShell, in the
# folder with homecore.exe, run it with nothing else and paste the command when asked:
#   .\homecore.exe --mode agent
# It creates "HomeCore Agent.lnk" (runs as administrator) and prints how to start it at sign-in
# (Register-ScheduledTask) and how to open the firewall for the local subnet (New-NetFirewallRule).
# Nothing is stored on the machine, so the working directory no longer matters.
# With a config file instead: run an ELEVATED console (Administrator) or a Task Scheduler task
# "Run with highest privileges". There is no Windows Service wrapper yet. Set data_dir explicitly: the
# default is ./data relative to the working directory (System32 when elevated).
#   homecore.exe --mode agent --config C:\ProgramData\homecore\agent.yaml
# Applications are closed by force on shutdown: unsaved data is lost.

Notes
- Distros with polkit < 0.106 (old Debian/Ubuntu) need an equivalent `.pkla` file instead of JS rules.
- Keep the agent port closed to the Internet (firewall to the LAN / VPN subnet).

## Port forwarding on ports below 1024 (optional)

Port forwarding rules (UI: **Port Forwarding**, [ADR-022](../docs/design/decisions.md)) listen from the Controller
process. The shipped unit drops every capability (`CapabilityBoundingSet=` is empty), so a rule on a port below 1024
(80, 443, 22 ...) shows `error: ... permission denied`. Ports from 1024 up need nothing. The unit is deliberately
left as it is; opt in with a drop-in:

```bash
sudo systemctl edit homecore-controller      # creates /etc/systemd/system/homecore-controller.service.d/override.conf
# [Service]
# AmbientCapabilities=CAP_NET_BIND_SERVICE
# CapabilityBoundingSet=CAP_NET_BIND_SERVICE
sudo systemctl restart homecore-controller   # enabled rules start again by themselves
```

A rule listening on `0.0.0.0` is reachable from every network the host is on: open the port in the host firewall
only for the networks that should use it, or pick a specific local interface in the rule. A rule that cannot bind is
retried every 15 seconds, so it also comes up when the port or the interface becomes available later.

## nginx in front of the Controller (optional)

[nginx/homecore.conf.template](nginx/homecore.conf.template) serves the built frontend (the `web/static`
output or the release frontend zip) and proxies `/api/`, `/healthz` and `/readyz` to the Controller:

- Frontend: gzip for text types; `/assets/*` (content-hashed) cached for 1 year + `immutable`, `index.html`
  `no-cache`, other root files 1 hour; SPA fallback to `index.html`; a missing `/assets/*` file is a real 404.
- `/api/`: forwards `Host`, `X-Forwarded-*`; sends `Upgrade`/`Connection` only for WebSocket handshakes
  (upstream keep-alive otherwise); 1 h read timeout so idle sockets survive.
- Security headers mirror the Controller's (`nosniff`, `DENY`, CSP without `font-src data:`).

```bash
export SERVER_NAME=home.example.com WEB_ROOT=/var/www/homecore BACKEND_ADDR=127.0.0.1:8080
envsubst '${SERVER_NAME} ${WEB_ROOT} ${BACKEND_ADDR}' < deploy/nginx/homecore.conf.template \
  | sudo tee /etc/nginx/conf.d/homecore.conf
sudo nginx -t && sudo systemctl reload nginx
```

Set `controller.trusted_proxies: ["127.0.0.1/32"]` (the proxy's address) in `homecore.yaml`, otherwise rate
limiting sees every client as the proxy. The Controller has no WebSocket endpoint yet (MVP scope); the proxy
is ready for one. Add TLS (commented block in the template) before exposing it beyond a trusted LAN.
