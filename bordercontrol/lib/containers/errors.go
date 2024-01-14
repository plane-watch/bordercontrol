package containers

import "errors"

var (
	ErrNotInitialised            = errors.New("container manager not initialised")
	ErrAlreadyInitialised        = errors.New("container manager already initialised")
	ErrContextCancelled          = errors.New("context cancelled")
	ErrTimeoutContainerStartReq  = errors.New("timeout waiting to submit container start request")
	ErrTimeoutContainerStartResp = errors.New("timeout waiting for container start response")
	ErrMultipleContainersFound   = errors.New("multiple containers found")
	ErrContainerNotFound         = errors.New("container not found")
)
