# Generator and consumer share an image; the compose service picks the script.
FROM python:3.12-slim

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1

WORKDIR /app

# Only the consumer needs a Kafka client; the generator is stdlib-only.
RUN pip install --no-cache-dir "confluent-kafka>=2.5,<3.0"

COPY alert_generator.py consumer.py ./

CMD ["python", "alert_generator.py"]
