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

# No build-essential by default: it adds ~254MB and nothing in this recipe
# needs a compiler (the npm globals below ship prebuilt binaries). A workspace
# that needs to compile native code can install it on demand via passwordless
# sudo, or bake it in through the EXTRA_PACKAGES build arg.
RUN apt-get update && apt-get install -y --no-install-recommends \
      bash ca-certificates curl file git jq nodejs npm \
      openssh-client passwd procps ripgrep sudo ${EXTRA_PACKAGES} \
    && rm -rf /var/lib/apt/lists/*

# User-provided baseImage.commands, run after package installation (a no-op ":"
# by default).
RUN ${EXTRA_COMMANDS}

RUN git --version && rg --version && jq --version && npx --version && uvx --version

# Install OpenCode and tokscale from npm (reliable, unlike the GitHub-API
# installer), only when absent so the step is a no-op on a base that already ships
# them.
# opencode-ai ships a ~160MB self-contained binary, but its package declares
# every platform variant as an optionalDependency and npm installs all of the
# ones matching this OS/arch (e.g. four linux-x64 builds: glibc/musl x avx2/
# baseline) — ~630MB for one usable binary. postinstall already copied the right
# one to bin/opencode.exe (the symlinked entrypoint; nothing re-selects at
# runtime), so the variant packages are dead weight. We re-pin bin/opencode.exe
# to the most portable variant for this arch and drop every variant package.
# Cache is cleaned in the same layer so the freed bytes never persist.
#
#   - x86_64 -> ...-x64-baseline[-musl]: the baseline build omits AVX2 so it runs
#     on any x64 CPU regardless of the build host's instruction set.
#   - aarch64 -> ...-arm64[-musl]: arm64 has no baseline/AVX2 split, so the plain
#     arm64 build is already the only/portable choice.
# The libc suffix matches the build base (musl on Alpine, glibc otherwise).
RUN set -eux; \
    command -v opencode >/dev/null 2>&1 || npm install -g opencode-ai; \
    command -v tokscale >/dev/null 2>&1 || npm install -g tokscale@latest; \
    OCM="$(npm root -g)/opencode-ai"; \
    libc=""; if [ -f /etc/alpine-release ] || ldd --version 2>&1 | grep -qi musl; then libc="-musl"; fi; \
    case "$(uname -m)" in \
      x86_64)        variant="opencode-linux-x64-baseline${libc}" ;; \
      aarch64|arm64) variant="opencode-linux-arm64${libc}" ;; \
      *)             variant="" ;; \
    esac; \
    if [ -n "$variant" ] && [ -x "$OCM/node_modules/$variant/bin/opencode" ]; then \
      cp -L "$OCM/node_modules/$variant/bin/opencode" "$OCM/bin/opencode.exe.real"; \
      mv -f "$OCM/bin/opencode.exe.real" "$OCM/bin/opencode.exe"; \
    fi; \
    rm -rf "$OCM"/node_modules/opencode-linux-* "$OCM"/node_modules/opencode-darwin-* "$OCM"/node_modules/opencode-windows-*; \
    npm cache clean --force; rm -rf "$HOME/.npm" /root/.npm /tmp/* /var/tmp/*; \
    opencode --version; command -v tokscale

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

ENV PATH=/usr/local/bin:/usr/local/sbin:/usr/sbin:/usr/bin:/sbin:/bin
