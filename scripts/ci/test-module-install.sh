#!/usr/bin/env bash
# Tests one module end-to-end inside the ocm-base image, the way the manager runs
# it: as an unprivileged user in the sudo group (passwordless sudo), with the
# module directory bind-mounted read-only at the container module root and the
# prompt values passed as OCM_* environment variables. It runs the module's
# install, verifies the expected client/LSP is on PATH (sourcing ~/.env first so
# toolchains that export their PATH there are visible), then runs uninstall — so a
# broken install, a missing tool, or a broken uninstall all fail the test.
#
# Usage: RUNTIME=docker BASE_REF=ocm-base:test scripts/ci/test-module-install.sh <category>/<name>
set -euo pipefail

ref="${1:?usage: test-module-install.sh <category>/<name>}"
category="${ref%%/*}"
name="${ref##*/}"

RUNTIME="${RUNTIME:-docker}"
BASE_REF="${BASE_REF:-docker.io/mroger78/ocm-base:latest}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
MODULES_DIR="$ROOT/modules"

if [ ! -x "$MODULES_DIR/$category/$name/install" ]; then
  echo "::error::no executable install script at modules/$category/$name/install"
  exit 1
fi

# Per-module test inputs (OCM_* prompt values) and the post-install verification.
# Inputs are deliberately credential-free: cloud/forge modules install and
# configure their client without real secrets, exercising the install path that
# matters here. Version-selecting language modules are pinned for a deterministic,
# quicker run.
declare -a ocm_env=()
verify='true'
case "$ref" in
  cloud/aws)        ocm_env=(OCM_PROFILE=ci); verify='command -v aws' ;;
  cloud/gcp)        ocm_env=(OCM_PROFILE=ci); verify='command -v gcloud' ;;
  cloud/outscale)   ocm_env=(OCM_PROFILE=ci); verify='command -v octl' ;;
  cloud/ovh)        ocm_env=(OCM_PROFILE=ovh-eu); verify='command -v ovhcloud' ;;
  cloud/scaleway)   ocm_env=(OCM_PROFILE=ci); verify='command -v scw' ;;
  infra/kubernetes) verify='command -v kubectl' ;;
  tools/git)        ocm_env=(OCM_NAME=ci-bot OCM_EMAIL=ci@example.com)
                    verify='command -v git && [ "$(git config --global user.email)" = ci@example.com ]' ;;
  tools/ssh)        ocm_env=(OCM_HOST=example.com)
                    verify='command -v ssh && test -f "$HOME/.ssh/config.d/example.com.conf"' ;;
  tools/github)     ocm_env=(OCM_HOSTNAME=github.com OCM_IMPORT_AUTH=no); verify='command -v gh' ;;
  tools/gitlab)     ocm_env=(OCM_HOSTNAME=gitlab.com OCM_IMPORT_AUTH=no); verify='command -v glab' ;;
  language/c)       verify='command -v gcc && command -v make && command -v gdb && command -v clangd' ;;
  language/golang)  ocm_env=(OCM_VERSION=1.23); verify='command -v go && command -v gopls && command -v dlv' ;;
  language/nodejs)  ocm_env=(OCM_VERSION=lts); verify='command -v node && command -v npm && command -v typescript-language-server' ;;
  language/python)  ocm_env=(OCM_VERSION=3.12); verify='command -v python && command -v pyright && command -v ruff' ;;
  *) echo "::warning::no test inputs defined for $ref; running install with defaults and no tool verification" ;;
esac

env_flags=(-e "OCM_TEST_CATEGORY=$category" -e "OCM_TEST_NAME=$name" -e "OCM_TEST_VERIFY=$verify")
for kv in "${ocm_env[@]}"; do env_flags+=(-e "$kv"); done

echo "==> testing module $ref"
"$RUNTIME" run --rm \
  -v "$MODULES_DIR:/opt/opencode-manager/modules:ro" \
  "${env_flags[@]}" \
  "$BASE_REF" \
  bash -euo pipefail -c '
    # Mirror the workspace runtime: an unprivileged user in the sudo group, which
    # the base image grants passwordless sudo. The module scripts run as this user.
    useradd -m -s /bin/bash -G sudo tester

    # Re-emit every OCM_* value (including the test category/name/verify) into a
    # script the unprivileged user runs. printf %q keeps values with spaces (the
    # verify command) intact across the privilege drop, without env-inheritance
    # ambiguity between runuser/su/sudo.
    run=/home/tester/run.sh
    {
      echo "#!/usr/bin/env bash"
      echo "set -euo pipefail"
      echo "export HOME=/home/tester"
      for v in $(compgen -e | grep "^OCM_"); do
        printf "export %s=%q\n" "$v" "${!v}"
      done
      cat <<"INNER"
mod="/opt/opencode-manager/modules/$OCM_TEST_CATEGORY/$OCM_TEST_NAME"
export OCM_MODULE="$OCM_TEST_NAME" OCM_HOME="$HOME"
echo "--- install ---"
OCM_PHASE=install "$mod/install"
echo "--- verify ---"
[ -f "$HOME/.env" ] && . "$HOME/.env"
eval "$OCM_TEST_VERIFY"
echo "--- uninstall ---"
OCM_PHASE=uninstall "$mod/uninstall"
INNER
    } > "$run"
    chmod +x "$run"
    chown tester:tester "$run"
    runuser -u tester -- "$run"
  '
echo "==> module $ref OK"
