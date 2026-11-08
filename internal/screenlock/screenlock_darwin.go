// SPDX-License-Identifier: MIT

//go:build darwin && cgo

package screenlock

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework Foundation
#import <Foundation/Foundation.h>

extern void goScreenLocked(void);

// watchScreenLock observes the distributed notification posted when the
// screen locks and runs the run loop of this thread (never returns).
static void watchScreenLock(void) {
	@autoreleasepool {
		[[NSDistributedNotificationCenter defaultCenter]
			addObserverForName:@"com.apple.screenIsLocked"
			object:nil
			queue:nil
			usingBlock:^(NSNotification *n) { goScreenLocked(); }];
	}
	CFRunLoopRun();
}
*/
import "C"

import (
	"context"
	"runtime"
	"sync"
)

var (
	mu       sync.Mutex
	started  bool
	watchers = map[*watcher]struct{}{}
)

type watcher struct{ onLock func() }

//export goScreenLocked
func goScreenLocked() {
	mu.Lock()
	var fs []func()
	for w := range watchers {
		fs = append(fs, w.onLock)
	}
	mu.Unlock()
	for _, f := range fs {
		go f()
	}
}

func watch(ctx context.Context, onLock func()) error {
	w := &watcher{onLock: onLock}
	mu.Lock()
	watchers[w] = struct{}{}
	if !started {
		started = true
		go func() {
			runtime.LockOSThread() // the observer and its run loop stay on this thread
			C.watchScreenLock()
		}()
	}
	mu.Unlock()
	go func() {
		<-ctx.Done()
		mu.Lock()
		delete(watchers, w)
		mu.Unlock()
	}()
	return nil
}
