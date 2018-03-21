FROM golang:1.8

COPY jiralert /go/src/github.com/anchorfree/jira-alerter
COPY glide.yaml /go/src/github.com/anchorfree/jira-alerter
RUN curl https://glide.sh/get | sh \
    && cd /go/src/github.com/anchorfree/jira-alerter \
    && glide install -v
RUN cd /go/src/github.com/anchorfree/jira-alerter && env CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -v -ldflags "-X main.Version=0.5" -o /build/jira-alerter github.com/anchorfree/jira-alerter/cmd/jiralert/

FROM alpine

COPY --from=0 /build/jira-alerter /

COPY entrypoint.sh /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
