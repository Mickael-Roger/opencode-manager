# Base image for opencode-manager workspaces, published as
# docker.io/mroger78/ocm-base.
#
# This is the single source of truth for the base recipe. It is a normal,
# directly buildable Dockerfile: the CI pipeline publishes it as-is (defaults
# below), and the opencode-manager binary builds it locally as a fallback when a
# user points baseImage.name at a custom distro, passing build args. Every install
# step is idempotent so the recipe can also layer on a base that already provides
# some of these tools without failing (e.g. "user already exists").
ARG BASE_IMAGE=debian:stable-slim
FROM ${BASE_IMAGE}

# Extra packages/commands from the user's baseImage config. Empty by default, so
# the published image carries only the standard tooling.
ARG EXTRA_PACKAGES=""
ARG EXTRA_COMMANDS=":"

COPY --from=ghcr.io/astral-sh/uv:latest /uv /uvx /usr/local/bin/

RUN apt-get update && apt-get install -y --no-install-recommends \
      bash build-essential ca-certificates curl file git jq nodejs npm \
      openssh-client passwd procps ripgrep sudo ${EXTRA_PACKAGES} \
    && rm -rf /var/lib/apt/lists/*

# User-provided baseImage.commands, run after package installation (a no-op ":"
# by default).
RUN ${EXTRA_COMMANDS}

# Create linuxbrew as a system account (UID/GID < 1000). Without an explicit id,
# useradd grabs UID 1000, which collides with the workspace user added at UID 1000
# in the workspace image layer.
RUN set -eux; getent group linuxbrew >/dev/null 2>&1 || groupadd -r linuxbrew; id -u linuxbrew >/dev/null 2>&1 || useradd -r -g linuxbrew -m -s /bin/bash linuxbrew; mkdir -p /home/linuxbrew/.linuxbrew/Homebrew /home/linuxbrew/.linuxbrew/bin; chown -R linuxbrew:linuxbrew /home/linuxbrew
RUN set -eux; if [ ! -x /home/linuxbrew/.linuxbrew/bin/brew ]; then su linuxbrew -c 'git clone --depth=1 https://github.com/Homebrew/brew /home/linuxbrew/.linuxbrew/Homebrew && ln -s ../Homebrew/bin/brew /home/linuxbrew/.linuxbrew/bin/brew'; fi; su linuxbrew -c '/home/linuxbrew/.linuxbrew/bin/brew --version'
RUN git --version && rg --version && jq --version && npx --version && uvx --version && su linuxbrew -c '/home/linuxbrew/.linuxbrew/bin/brew --version'

# Install OpenCode and tokscale from npm (reliable, unlike the GitHub-API
# installer), only when absent so the step is a no-op on a base that already ships
# them.
RUN set -eux; command -v opencode >/dev/null 2>&1 || npm install -g opencode-ai; opencode --version
RUN set -eux; command -v tokscale >/dev/null 2>&1 || npm install -g tokscale@latest; command -v tokscale

# Manager scripts: the attach wrapper and the supervisor entrypoint (PID 1). They
# are COPYed rather than written via heredoc, which the buildah builder used by
# Podman does not support.
COPY opencode-manager-attach /usr/local/bin/opencode-manager-attach
COPY opencode-manager-entrypoint /usr/local/bin/opencode-manager-entrypoint
RUN chmod 0755 /usr/local/bin/opencode-manager-attach /usr/local/bin/opencode-manager-entrypoint

# Passwordless sudo for the sudo group: module install scripts run as the
# unprivileged workspace user but need root for system packages.
RUN echo '%sudo ALL=(ALL) NOPASSWD:ALL' > /etc/sudoers.d/opencode-manager && chmod 0440 /etc/sudoers.d/opencode-manager

# Source the per-workspace ~/.env from interactive shells, mirroring the server.
RUN grep -qF '[ -f "$HOME/.env" ]' /etc/bash.bashrc || echo '[ -f "$HOME/.env" ] && . "$HOME/.env"' >> /etc/bash.bashrc

ENV PATH=/home/linuxbrew/.linuxbrew/bin:/home/linuxbrew/.linuxbrew/sbin:/usr/local/bin:/usr/local/sbin:/usr/sbin:/usr/bin:/sbin:/bin
