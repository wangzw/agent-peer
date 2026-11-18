//go:build !linux

package node

import "errors"

// ListProcs is only implemented on Linux.
func ListProcs(func(Proc) bool) ([]Proc, error) {
	return nil, errors.ErrUnsupported
}

// Ancestors is only implemented on Linux.
func Ancestors(pid int) ([]int, error) {
	return nil, errors.ErrUnsupported
}
