SERVICE := shadowtwin-forwarder
VENV    := .venv
PY      := $(VENV)/bin/python

.PHONY: help venv dev install uninstall restart status logs health validate lint test

help:
	@echo "make venv       create local virtualenv with runtime dependencies"
	@echo "make dev        venv + development tools (ruff)"
	@echo "make install    install + enable + start the systemd service (sudo)"
	@echo "make uninstall  stop + disable + remove the systemd service (sudo)"
	@echo "make restart    restart the service"
	@echo "make status     show service status"
	@echo "make logs       follow the service journal"
	@echo "make health     run the health check (exit 0 = healthy)"
	@echo "make validate   print every operational check"
	@echo "make lint       ruff over the code base"
	@echo "make test       run the smoke test suite"

venv:
	python3 -m venv $(VENV)
	$(PY) -m pip install --quiet --upgrade pip
	$(PY) -m pip install --quiet -r requirements.txt

dev: venv
	$(PY) -m pip install --quiet ruff

install:
	sudo ./install_service.sh

uninstall:
	sudo ./uninstall_service.sh

restart:
	sudo systemctl restart $(SERVICE)

status:
	systemctl status $(SERVICE) --no-pager

logs:
	journalctl -u $(SERVICE) -f

health:
	$(PY) app.py --health

validate:
	$(PY) app.py --validate-config

lint:
	$(VENV)/bin/ruff check .

test:
	$(PY) tests/smoke_test.py
