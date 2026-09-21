FROM python:3.12-slim

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1

WORKDIR /app

COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY app.py normalize.py store.py schema.sql ./

RUN useradd --system --uid 10002 --user-group ingestor
USER ingestor

CMD ["python", "app.py"]
