FROM alpine:3.9
COPY build/fs_exporter /
CMD /fs_exporter
