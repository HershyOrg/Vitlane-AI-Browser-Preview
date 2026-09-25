#!/usr/bin/env bash
set -euo pipefail

# CI and Release run the same real-restic restore selection tests. Keep their
# dependency setup identical, with bounded mirror waits and signed Ubuntu sources.
test -f /etc/apt/sources.list.d/ubuntu.sources
sudo sed -i 's|http://azure.archive.ubuntu.com/ubuntu|https://archive.ubuntu.com/ubuntu|g' /etc/apt/apt-mirrors.txt
apt_options=(
  -o Dir::Etc::sourcelist=/etc/apt/sources.list.d/ubuntu.sources
  -o Dir::Etc::sourceparts=-
  -o Acquire::http::Timeout=10
  -o Acquire::https::Timeout=10
  -o Acquire::Retries=2
)
sudo apt-get "${apt_options[@]}" update
sudo apt-get "${apt_options[@]}" install --yes restic
restic version
