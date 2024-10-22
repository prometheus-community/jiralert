FROM golang:1.23 AS builder
WORKDIR /go/src/github.com/prometheus-community/jiralert
COPY . /go/src/github.com/prometheus-community/jiralert
RUN GO111MODULE=on GOBIN=/tmp/bin make

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /go/src/github.com/prometheus-community/jiralert/jiralert /bin/jiralert

ENTRYPOINT [ "/bin/jiralert" ]
