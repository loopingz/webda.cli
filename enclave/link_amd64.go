//go:build darwin && cgo && sebridge && amd64

package enclave

// #cgo LDFLAGS: -L${SRCDIR} -lsebridge_amd64
import "C"
