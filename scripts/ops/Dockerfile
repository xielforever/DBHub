FROM postgres:16-alpine@sha256:721873c34ceb9f8d8fc265984940dc982404c105f19ad51be9fdc5970a6080ea
RUN mkdir -p /ops /var/lib/dbhub/backups \
 && chown 65532:65532 /var/lib/dbhub/backups \
 && chmod 700 /var/lib/dbhub/backups \
 && command -v flock && command -v timeout
COPY --chmod=0555 backup-metadata.sh backup-loop.sh backup-health.sh restore-metadata.sh /ops/
COPY --chmod=0444 verify-metadata.sql /ops/
USER 65532:65532
HEALTHCHECK --interval=60s --timeout=5s --start-period=30m --retries=2 CMD ["sh","/ops/backup-health.sh"]
ENTRYPOINT ["sh","/ops/backup-loop.sh"]
