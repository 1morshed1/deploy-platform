# Build for a Python application.
#
# Python is interpreted, so there's no compile step to separate — but we still
# use a slim base and install dependencies in their own cached layer.
FROM python:3.12-slim
WORKDIR /app

# System certs + build essentials only if a dependency needs to compile a wheel.
# Kept minimal to keep the image small.
ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

# Install dependencies first for layer caching.
COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt

COPY . .
EXPOSE 8000
# Assumes a WSGI/ASGI entrypoint; gunicorn is a common production server. Users
# whose app differs can commit their own Dockerfile (see learning doc 03).
CMD ["python", "main.py"]
