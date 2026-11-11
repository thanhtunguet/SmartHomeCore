# Deployment (Linux)

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
```

Notes
- Distros with polkit < 0.106 (old Debian/Ubuntu) need an equivalent `.pkla` file instead of JS rules.
- Keep the agent port closed to the Internet (firewall to the LAN / VPN subnet).
