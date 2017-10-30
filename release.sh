#!/bin/bash
# a hack to generate releases like other prometheus projects
# use like this: 
#       VERSION=1.0.1 ./release.sh

VERSION=`git describe --tags 2>/dev/null`
VERSION=${VERSION:-`git rev-parse --short HEAD`}

RELEASE="jiralert-$VERSION.linux-amd64"

rm -rf "bin/$RELEASE"
mkdir -p "bin/$RELEASE"

echo Building...
env GOOS=linux GOARCH=amd64 go build -ldflags "-X main.Version=$VERSION" -o "bin/$RELEASE/jiralert" github.com/alin-sinpalean/jiralert/cmd/jiralert

echo Packaging...
cp LICENSE "bin/$RELEASE"
mkdir -p "bin/$RELEASE/config"
cp config/* "bin/$RELEASE/config"
pushd bin >/dev/null
tar -zcvf "../$RELEASE.tar.gz" "$RELEASE"
popd >/dev/null
rm -rf "bin/$RELEASE"

echo Done.