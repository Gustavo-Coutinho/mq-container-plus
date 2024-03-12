#!/bin/bash

BUILDER_IMAGE=$1

if [ "$BUILDER_IMAGE" == "bitnami/golang" ]; then
    apt-get update && apt-get install -y apache2-dev libapr1-dev libaprutil1-dev && \
    ln -s /usr/include/apr-1.0 /usr/include/apr-1
elif [ "$BUILDER_IMAGE" == "registry.access.redhat.com/ubi8/go-toolset" ]; then
    yum --assumeyes --disableplugin=subscription-manager install apr-devel apr-util-openssl apr-util-devel
else
    echo "Unsupported BUILDER_IMAGE value: $BUILDER_IMAGE"
    exit 1
fi