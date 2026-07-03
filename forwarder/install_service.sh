#!/usr/bin/env bash
# Install the ShadowTwin Forwarder as a systemd service.
#
# Detects the project directory (wherever this checkout lives), creates the
# service user and virtualenv, installs the config and unit file, then
# enables and starts the service and verifies it came up.
#
# Usage: sudo ./install_service.sh
set -euo pipefail

SERVICE_NAME="shadowtwin-forwarder"
PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
VENV_DIR="${PROJECT_DIR}/.venv"
UNIT_SRC="${PROJECT_DIR}/systemd/${SERVICE_NAME}.service"
UNIT_DST="/etc/systemd/system/${SERVICE_NAME}.service"
CONFIG_DIR="/etc/${SERVICE_NAME}"
CONFIG_DST="${CONFIG_DIR}/config.yaml"
SERVICE_USER="shadowtwin"

log()  { echo "[install] $*"; }
fail() { echo "[install] ERROR: $*" >&2; exit 1; }

[[ ${EUID} -eq 0 ]] || fail "must run as root: sudo ./install_service.sh"
[[ -f "${PROJECT_DIR}/app.py" ]] || fail "app.py not found in ${PROJECT_DIR}"
[[ -f "${UNIT_SRC}" ]] || fail "unit file ${UNIT_SRC} not found"
command -v python3 >/dev/null || fail "python3 is not installed"

# 1. Service user (no shell, no home creation)
if ! id -u "${SERVICE_USER}" >/dev/null 2>&1; then
    log "creating system user '${SERVICE_USER}'"
    useradd --system --shell /usr/sbin/nologin \
        --home-dir "${PROJECT_DIR}" --no-create-home "${SERVICE_USER}"
fi
if getent group wazuh >/dev/null; then
    usermod -aG wazuh "${SERVICE_USER}"
    log "user '${SERVICE_USER}' is in group 'wazuh' (read access to /var/ossec/logs)"
else
    log "WARNING: group 'wazuh' does not exist on this host."
    log "         The service user must still be able to read the watched files."
fi

# 2. Virtualenv + dependencies
if [[ ! -x "${VENV_DIR}/bin/python" ]]; then
    log "creating virtualenv in ${VENV_DIR}"
    python3 -m venv "${VENV_DIR}"
fi
log "installing dependencies"
"${VENV_DIR}/bin/pip" install --quiet --upgrade pip
"${VENV_DIR}/bin/pip" install --quiet -r "${PROJECT_DIR}/requirements.txt"

# 3. Configuration — never overwrite an existing one
mkdir -p "${CONFIG_DIR}"
if [[ ! -f "${CONFIG_DST}" ]]; then
    log "installing default config to ${CONFIG_DST}"
    install -m 0644 "${PROJECT_DIR}/config.yaml" "${CONFIG_DST}"
else
    log "keeping existing ${CONFIG_DST}"
fi

# 4. Unit file, with paths rewritten to this checkout
log "installing ${UNIT_DST} (project dir: ${PROJECT_DIR})"
sed "s|/opt/shadowtwin-forwarder|${PROJECT_DIR}|g" "${UNIT_SRC}" > "${UNIT_DST}"
if ! getent group wazuh >/dev/null; then
    sed -i "/^SupplementaryGroups=wazuh/d" "${UNIT_DST}"
fi
chmod 0644 "${UNIT_DST}"

# 5. Enable and start
systemctl daemon-reload
systemctl enable "${SERVICE_NAME}" >/dev/null
systemctl restart "${SERVICE_NAME}"

# 6. Verify
sleep 3
if systemctl is-active --quiet "${SERVICE_NAME}"; then
    log "service is active"
else
    systemctl status "${SERVICE_NAME}" --no-pager || true
    fail "service failed to start; inspect: journalctl -u ${SERVICE_NAME} -e"
fi

log "running health check (informational)"
if (cd "${PROJECT_DIR}" && sudo -u "${SERVICE_USER}" \
        "${VENV_DIR}/bin/python" app.py --health --config "${CONFIG_DST}"); then
    log "health check passed"
else
    log "WARNING: health check reported problems (see above)."
    log "         The service stays up and retries; fix the cause when convenient."
fi

echo
systemctl status "${SERVICE_NAME}" --no-pager --lines=5 || true
echo
log "done. Useful commands:"
log "  systemctl status ${SERVICE_NAME}"
log "  journalctl -u ${SERVICE_NAME} -f"
log "  sudo systemctl restart ${SERVICE_NAME}"
