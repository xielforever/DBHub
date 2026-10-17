# syntax=docker/dockerfile:1
FROM debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587 AS build
WORKDIR /src
RUN --mount=type=secret,id=proxy_ca,required=true,mode=0444 \
    set -eu; export http_proxy="${HTTP_PROXY:-}" https_proxy="${HTTPS_PROXY:-}" no_proxy="${NO_PROXY:-}"; sed -i 's|http://deb.debian.org|https://deb.debian.org|g' /etc/apt/sources.list.d/debian.sources; \
    if [ -f /run/secrets/proxy_ca ]; then export SSL_CERT_FILE=/run/secrets/proxy_ca; export dbhub_ca_option="-o Acquire::https::CaInfo=/run/secrets/proxy_ca"; else export dbhub_ca_option=""; fi; \
    apt-get $dbhub_ca_option update -q; apt-get $dbhub_ca_option install -y -qq --no-install-recommends openjdk-17-jdk-headless python3 ca-certificates; rm -rf /var/lib/apt/lists/*
COPY artifacts.lock.json fetch-artifacts.py ./
RUN --mount=type=secret,id=proxy_ca,required=true,mode=0444 \
    export http_proxy="${HTTP_PROXY:-}" https_proxy="${HTTPS_PROXY:-}" no_proxy="${NO_PROXY:-}"; \
    if [ -f /run/secrets/proxy_ca ]; then export SSL_CERT_FILE=/run/secrets/proxy_ca; fi; \
    python3 fetch-artifacts.py artifacts.lock.json /out/lib
COPY src/DBHubJdbc.java ./
RUN mkdir /out/classes && javac -encoding UTF-8 -cp '/out/lib/*' -d /out/classes DBHubJdbc.java

FROM debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587
RUN --mount=type=secret,id=proxy_ca,required=true,mode=0444 \
    set -eu; export http_proxy="${HTTP_PROXY:-}" https_proxy="${HTTPS_PROXY:-}" no_proxy="${NO_PROXY:-}"; sed -i 's|http://deb.debian.org|https://deb.debian.org|g' /etc/apt/sources.list.d/debian.sources; \
    if [ -f /run/secrets/proxy_ca ]; then export dbhub_ca_option="-o Acquire::https::CaInfo=/run/secrets/proxy_ca"; else export dbhub_ca_option=""; fi; \
    apt-get $dbhub_ca_option update -q; apt-get $dbhub_ca_option install -y -qq --no-install-recommends openjdk-17-jre-headless curl ca-certificates; rm -rf /var/lib/apt/lists/*
WORKDIR /opt/jdbc
COPY --from=build /out/ ./
COPY artifacts.lock.json ./
ENV JDBC_ARTIFACT_LOCK=/opt/jdbc/artifacts.lock.json JDBC_ARTIFACT_DIR=/opt/jdbc/lib
USER 10001:10001
EXPOSE 8090
HEALTHCHECK --interval=15s --timeout=3s --start-period=15s CMD curl --fail --silent http://127.0.0.1:8090/health >/dev/null || exit 1
ENTRYPOINT ["java","-Xms32m","-Xmx256m","-cp","/opt/jdbc/classes:/opt/jdbc/lib/gson-2.13.2.jar","DBHubJdbc"]
