#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

# Runs the local "five node network" script. 
# After the network becomes healthy, stop the network and verify that no avalanchego processes are running. 
# If the network never becomes healthy, or if avalanchego processes are left lingering, the Github Action should fail.


# Start the local "five node network":
go run examples/local/fivenodenetwork/main.go
