#!/usr/bin/env bash
# Remove the ShadowTwin Forwarder systemd service.
#
# By default the config (/etc/shadowtwin-forwarder), state
# (/var/lib/shadowtwin-forwarder), logs (/var/log/shadowtwin-forwarder) and
# the service user are kept, so a later reinstall resumes from the same
# offsets. Pass --purge to remove all of that too.
#
# Usage: sudo ./uninstall_service.sh [--purge]
set -euo pipefail

SERVICE_NAME="shadowtwin-forwarder"
UNIT_DST="/etc/systemd/system/${SERVICE_NAME}.service"
SERVICE_USER="shadowtwin"
PURGE=false
[[ "${1:-}" == "--purge" ]] && PURGE=true

log()  { echo "[uninstall] $*"; }
fail() { echo "[uninstall] ERROR: $*" >&2; exit 1; }

[[ ${EUID} -eq 0 ]] || fail "must run as root: sudo ./uninstall_service.sh"

log "stopping service"
systemctl stop "${SERVICE_NAME}" 2>/dev/null || true
log "disabling service"
systemctl disable "${SERVICE_NAME}" 2>/dev/null || true

if [[ -f "${UNIT_DST}" ]]; then
    rm -f "${UNIT_DST}"
    log "removed ${UNIT_DST}"
fi
systemctl daemon-reload
systemctl reset-failed "${SERVICE_NAME}" 2>/dev/null || true

if ${PURGE}; then
    log "purging config, state, logs and service user"
    rm -rf "/etc/${SERVICE_NAME}" "/var/lib/${SERVICE_NAME}" "/var/log/${SERVICE_NAME}"
    userdel "${SERVICE_USER}" 2>/dev/null || true
else
    log "kept (reinstall resumes from the same offsets):"
    log "  /etc/${SERVICE_NAME}/          configuration"
    log "  /var/lib/${SERVICE_NAME}/      offset state"
    log "  /var/log/${SERVICE_NAME}/      application logs"
    log "  user '${SERVICE_USER}'"
    log "run with --purge to remove them as well"
fi

log "done"
