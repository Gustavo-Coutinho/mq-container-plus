#!/bin/bash
# -*- mode: sh -*-
# © Copyright IBM Corporation 2015, 2023
#
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Fail on any non-zero return code
set -ex

if [ -f /usr/bin/yum ]; then
  YUM=true
else
  YUM=false
fi

if [ -f /usr/bin/microdnf ]; then
  MICRODNF=true
else
  MICRODNF=false
fi

if [ -f /usr/bin/rpm ]; then
  RPM=true
else
  RPM=false
fi

if [ -f /usr/bin/apt-get ]; then
  UBUNTU=true
else
  UBUNTU=false
fi

CPU_ARCH=$(uname -m)

if $UBUNTU; then
  export DEBIAN_FRONTEND=noninteractive
  # Use a reduced set of apt repositories.
  # This ensures no unsupported code gets installed, and makes the build faster
  source /etc/os-release
  # Figure out the correct apt URL based on the CPU architecture
  if [ "${CPU_ARCH}" == "x86_64" ]; then
     APT_URL="http://archive.ubuntu.com/ubuntu/"
  else
     APT_URL="http://ports.ubuntu.com/ubuntu-ports/"
  fi
  # Use a reduced set of apt repositories.
  # This ensures no unsupported code gets installed, and makes the build faster
  echo "deb ${APT_URL} ${UBUNTU_CODENAME} main restricted" > /etc/apt/sources.list
  echo "deb ${APT_URL} ${UBUNTU_CODENAME}-updates main restricted" >> /etc/apt/sources.list
  echo "deb ${APT_URL} ${UBUNTU_CODENAME}-security main restricted" >> /etc/apt/sources.list
  # Install additional packages required by MQ, this install process and the runtime scripts
  EXTRA_DEBS="bash bc ca-certificates coreutils curl debianutils file findutils gawk grep libc-bin mount passwd procps sed tar util-linux"
  apt-get update
  apt-get install -y --no-install-recommends ${EXTRA_DEBS}
  # Apply any bug fixes not included in base Ubuntu or MQ image.
  # Don't upgrade everything based on Docker best practices https://docs.docker.com/engine/userguide/eng-image/dockerfile_best-practices/#run
  apt-get install -y libapparmor1 libapr1-dev libsystemd0 systemd systemd-sysv libudev1 perl-base --only-upgrade
  # End of bug fixes
  # Clean up cached files
  rm -rf /var/lib/apt/lists/*
fi

if $RPM; then
  EXTRA_RPMS="bash bc ca-certificates file findutils gawk glibc-common grep ncurses-compat-libs passwd procps-ng sed shadow-utils tar util-linux which"
  # Install additional packages required by MQ, this install process and the runtime scripts
  if $YUM; then
    yum -y install --setopt install_weak_deps=false ${EXTRA_RPMS}
    yum -y clean all
    # Clean up cached files
    rm -rf /var/cache/yum/*
  fi
  
  if $MICRODNF; then
    microdnf --disableplugin=subscription-manager install ${EXTRA_RPMS}
    # Clean up cached files
    microdnf --disableplugin=subscription-manager clean all
  fi
fi