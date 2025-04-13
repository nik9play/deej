#!/bin/sh

echo 'Building deej (all)...'

./pkg/deej/scripts/linux/build-dev.sh
./pkg/deej/scripts/linux/build-release.sh
