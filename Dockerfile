FROM alpine:3.9

RUN apk update && \
    apk -Uuv add dumb-init ca-certificates && \
    rm /var/cache/apk/*
COPY run.sh /run.sh
RUN touch env.sh && chmod 755 /run.sh
COPY build/fs_exporter /fs_exporter

ENTRYPOINT ["/run.sh"]
CMD ["/fs_exporter"]

