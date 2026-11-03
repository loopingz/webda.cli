//go:build darwin && cgo && sebridge && arm64

package enclave

// #cgo LDFLAGS: -L${SRCDIR} -lsebridge_arm64
import "C"
