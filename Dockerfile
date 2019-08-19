FROM circleci/golang:1.12 AS builder
COPY . /go/src/github.com/free/jiralert
RUN mkdir -p /go/src/github.com/free/jiralert && sudo chown -R circleci:circleci /go/src/github.com/free/
WORKDIR /go/src/github.com/free/jiralert
RUN GO111MODULE=on GOBIN=/tmp/bin make

FROM quay.io/prometheus/busybox:latest

COPY --from=builder /go/src/github.com/free/jiralert/jiralert /bin/jiralert

ENTRYPOINT [ "/bin/jiralert" ]

