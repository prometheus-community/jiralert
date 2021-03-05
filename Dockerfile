FROM circleci/golang:1.14 AS builder
COPY . /go/src/github.com/prometheus-community/jiralert
RUN mkdir -p /go/src/github.com/prometheus-community/jiralert && sudo chown -R circleci:circleci /go/src/github.com/prometheus-community/
WORKDIR /go/src/github.com/prometheus-community/jiralert
RUN GO111MODULE=on GOBIN=/tmp/bin make

FROM quay.io/prometheus/busybox-linux-amd64:latest

COPY --from=builder /go/src/github.com/prometheus-community/jiralert/jiralert /bin/jiralert

ENTRYPOINT [ "/bin/jiralert" ]

