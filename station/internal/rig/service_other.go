//go:build !linux

package rig

import "context"

func newServiceReader() *ServiceReader { return &ServiceReader{} }
func readService(context.Context, *ServiceReader, string, Endpoint) (ServiceObservation, error) {
	return ServiceObservation{}, ErrServiceUnavailable
}
