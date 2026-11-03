//go:build darwin && cgo

package presence

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework LocalAuthentication -framework Foundation
#import <LocalAuthentication/LocalAuthentication.h>
#include <stdlib.h>

// 0 confirmed, 1 denied or failed, 2 unavailable
static int webdaCanCheck(void) {
	@autoreleasepool {
		LAContext *ctx = [[LAContext alloc] init];
		return [ctx canEvaluatePolicy:LAPolicyDeviceOwnerAuthentication error:nil] ? 0 : 2;
	}
}

static int webdaCheck(const char *reason) {
	@autoreleasepool {
		LAContext *ctx = [[LAContext alloc] init];
		NSError *err = nil;
		if (![ctx canEvaluatePolicy:LAPolicyDeviceOwnerAuthentication error:&err]) {
			return 2;
		}
		__block int result = 1;
		dispatch_semaphore_t done = dispatch_semaphore_create(0);
		[ctx evaluatePolicy:LAPolicyDeviceOwnerAuthentication
		    localizedReason:[NSString stringWithUTF8String:reason]
		              reply:^(BOOL ok, NSError *e) {
			result = ok ? 0 : 1;
			dispatch_semaphore_signal(done);
		}];
		dispatch_semaphore_wait(done, DISPATCH_TIME_FOREVER);
		return result;
	}
}
*/
import "C"

import "unsafe"

func supported() bool { return C.webdaCanCheck() == 0 }

func check(reason string) error {
	r := C.CString(reason)
	defer C.free(unsafe.Pointer(r))
	switch C.webdaCheck(r) {
	case 0:
		return nil
	case 2:
		return ErrUnsupported
	default:
		return ErrDenied
	}
}
