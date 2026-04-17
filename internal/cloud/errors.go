package cloud

import "errors"

var (
	ErrNotFound         = errors.New("cloud: not found")
	ErrUnauthenticated  = errors.New("cloud: unauthenticated")
	ErrPermissionDenied = errors.New("cloud: permission denied")
	ErrRateLimited      = errors.New("cloud: rate limited")
	ErrProviderNotFound = errors.New("cloud: provider not registered")
)
