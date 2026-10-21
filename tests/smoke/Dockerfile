FROM debian:trixie-slim
RUN apt-get -o Acquire::ForceIPv4=true -o Acquire::http::Timeout=30 -o Acquire::Retries=1 update && apt-get -o Acquire::ForceIPv4=true -o Acquire::http::Timeout=30 -o Acquire::Retries=1 install -y --no-install-recommends ca-certificates python3 openssl s6 nftables iproute2 shellcheck && rm -rf /var/lib/apt/lists/* && ln -s /usr/bin /command
COPY .smoke/ /candidate/
COPY tests/smoke/run.py /smoke.py
COPY install.sh /checks/install.sh
COPY scripts/*.sh /checks/scripts/
RUN ln -s /candidate/rw-core /usr/local/bin/rw-core && cp /candidate/*.dat /usr/local/bin/
RUN groupadd --gid 10001 gowave-node \
    && useradd --uid 10001 --gid gowave-node --no-create-home gowave-node \
    && chown -R gowave-node:gowave-node /candidate
USER 10001:10001
ENTRYPOINT ["python3", "/smoke.py"]
