// Copyright (C) 2022, Ava Labs, Inc. All rights reserved.
// See the file LICENSE for licensing terms.

package backend

type NodeConfig struct {
	Name       string                 `json:"name"`       // Name of the node
	Executable string                 `json:"executable"` // Executable - docker image in this context
	Config     map[string]interface{} `json:"config"`     // Config string to be passed in via --config-file-content
	NodeID     string                 `json:"nodeID"`     // If non-empty, this contains the pre-configured nodeID of the node
}
