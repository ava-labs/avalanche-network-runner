#!/usr/bin/env bash
set -e

if ! [[ "$0" =~ scripts/genmocks.sh ]]; then
  echo "must be run from repository root"
  exit 255
fi

mockery --dir api --name Client --output api/mocks/ --filename client.go
mockery --dir api --name EthClient --output api/mocks/ --filename EthClient.go
mockery --dir local --name NodeProcess --output local/mocks/ --filename node_process.go

echo "Successfully generated mock files"
