//go:build darwin && cgo && sebridge && manual

package enclave

import "testing"

// TestHardware seals with the real Secure Enclave and opens with Touch ID (approve the prompt):
//
//	scripts/build-sebridge.sh arm64 && CGO_CFLAGS=-mmacosx-version-min=12.0 \
//	CGO_LDFLAGS="-mmacosx-version-min=12.0 -L$(dirname $(dirname $(xcrun --find swiftc)))/lib/swift/macosx" \
//	go test -tags 'sebridge manual' -run Hardware -v ./enclave
func TestHardware(t *testing.T) {
	if !Available() {
		t.Fatal("no Secure Enclave")
	}
	token, err := Seal([]byte("session.key"))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := Open(token, "check the webda-cli Secure Enclave bridge")
	if err != nil || string(plain) != "session.key" {
		t.Fatalf("%q %v", plain, err)
	}
}
