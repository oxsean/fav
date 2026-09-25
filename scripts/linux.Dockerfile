# Linux target for scripts/test-hosts.sh:
#   docker build -t tend-linux - < scripts/linux.Dockerfile
#   docker run -d --name tend-linux --restart unless-stopped tend-linux
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl git && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL https://mise.run | MISE_INSTALL_PATH=/usr/local/bin/mise sh
CMD ["sleep", "infinity"]
