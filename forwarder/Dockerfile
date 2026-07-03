FROM python:3.12-slim

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY app.py checks.py config.py logger.py producer.py state.py version.py watcher.py ./

# Non-root user; add the host's wazuh GID at runtime (compose: group_add)
# so /var/ossec/logs stays readable.
RUN useradd --system --uid 10001 --user-group shadowtwin \
    && mkdir -p /var/lib/shadowtwin-forwarder /var/log/shadowtwin-forwarder \
    && chown shadowtwin:shadowtwin /var/lib/shadowtwin-forwarder /var/log/shadowtwin-forwarder

USER shadowtwin

ENTRYPOINT ["python", "app.py"]
CMD ["--config", "/etc/shadowtwin-forwarder/config.yaml"]
