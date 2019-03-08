FROM quay.io/prometheus/busybox:latest

COPY jiralert /bin/jiralert

ENTRYPOINT [ "/bin/jiralert" ]

