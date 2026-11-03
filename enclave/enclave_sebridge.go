//go:build darwin && cgo && sebridge

package enclave

/*
#cgo LDFLAGS: -L/usr/lib/swift -Wl,-rpath,/usr/lib/swift -framework CryptoKit -framework Security -framework Foundation -framework LocalAuthentication
#include <stdint.h>
#include <stdlib.h>
int32_t webda_se_available(void);
int32_t webda_se_create(uint8_t *blob, int32_t blobCap, uint8_t *pub);
int32_t webda_se_agree(const uint8_t *blob, int32_t blobLen, const uint8_t *peer, const char *reason, uint8_t *out);
*/
import "C"

import (
	"fmt"
	"unsafe"
)

type secureEnclave struct{}

func init() { se = secureEnclave{} }

func (secureEnclave) available() bool { return C.webda_se_available() == 1 }

func (secureEnclave) create() ([]byte, []byte, error) {
	blob := make([]byte, 4096)
	pub := make([]byte, 65)
	n := C.webda_se_create((*C.uint8_t)(unsafe.Pointer(&blob[0])), C.int32_t(len(blob)), (*C.uint8_t)(unsafe.Pointer(&pub[0])))
	if n <= 0 {
		return nil, nil, fmt.Errorf("secure enclave key creation failed (%d)", int(n))
	}
	return blob[:n], pub, nil
}

func (secureEnclave) agree(blob, peer []byte, reason string) ([]byte, error) {
	if len(blob) == 0 || len(peer) != 65 {
		return nil, fmt.Errorf("invalid key")
	}
	r := C.CString(reason)
	defer C.free(unsafe.Pointer(r))
	out := make([]byte, 32)
	switch rc := C.webda_se_agree((*C.uint8_t)(unsafe.Pointer(&blob[0])), C.int32_t(len(blob)),
		(*C.uint8_t)(unsafe.Pointer(&peer[0])), r, (*C.uint8_t)(unsafe.Pointer(&out[0]))); rc {
	case 0:
		return out, nil
	case 1:
		return nil, ErrDenied
	default:
		return nil, fmt.Errorf("secure enclave key agreement failed (%d)", int(rc))
	}
}
