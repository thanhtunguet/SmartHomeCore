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

# Agent (on each managed machine)
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

Notes
- Distros with polkit < 0.106 (old Debian/Ubuntu) need an equivalent `.pkla` file instead of JS rules.
- Keep the agent port closed to the Internet (firewall to the LAN / VPN subnet).
