#!/usr/bin/env bash
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"

rm -f /etc/apt/sources.list.d/docker.list
apt-get update
apt-get install -y ca-certificates curl gnupg ffmpeg tmux unzip jq python3
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --batch --yes --dearmor -o /etc/apt/keyrings/docker.gpg
chmod a+r /etc/apt/keyrings/docker.gpg
printf 'deb [arch=%s signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu %s stable\n' "$(dpkg --print-architecture)" "$(. /etc/os-release && echo "$VERSION_CODENAME")" > /etc/apt/sources.list.d/docker.list
curl -fsSL https://deb.nodesource.com/setup_24.x | bash -
apt-get update
apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin nodejs
systemctl enable --now docker
# claude and pi are managed per user under ~/.local by the deploy and the cli-update timer; never
# install them system-wide (an old root copy silently wins when the managed one is missing).
npm install -g agent-browser@latest
id -u video-studio >/dev/null 2>&1 || useradd --system --create-home --home-dir /var/lib/video-studio --shell /usr/sbin/nologin video-studio
bash "$script_dir/../../common/install-rootless-docker.sh" video-studio
runuser -u video-studio -- env HOME=/var/lib/video-studio npx --yes hyperframes@0.8.6 browser ensure
test -x "$(runuser -u video-studio -- env HOME=/var/lib/video-studio npx --yes hyperframes@0.8.6 browser path | tail -n 1)"
bash "$script_dir/../../../agent_go/scripts/install-slack-cli.sh" /usr/local
command -v slack
command -v agent-browser
