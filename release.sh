#!/usr/bin/env bash
# a hack to generate releases like other prometheus projects
# use like this: 
#       VERSION=1.0.1 ./release.sh


rm -rf "bin/sachet-$VERSION.linux-amd64"
mkdir "bin/sachet-$VERSION.linux-amd64"
env GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o "bin/sachet-$VERSION.linux-amd64/sachet" github.com/messagebird/sachet/cmd/sachet
cd bin
tar -zcvf "sachet-$VERSION.linux-amd64.tar.gz" "sachet-$VERSION.linux-amd64"
