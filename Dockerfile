# Use Go 1.26 as requested
FROM golang:1.26

WORKDIR /app
COPY . .
RUN make build

ENV PORT=9098
EXPOSE ${PORT}

ENTRYPOINT ["./jiralert"]
CMD ["-config", "config/jiralert.yml", "-log.level", "debug", "-listen-address=:${PORT}", "-hash-jira-label"]