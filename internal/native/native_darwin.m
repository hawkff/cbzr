//go:build darwin && cgo

#import <Cocoa/Cocoa.h>
#import <CoreGraphics/CoreGraphics.h>
#import <objc/runtime.h>
#include <math.h>
#include <stdint.h>
#include <stdlib.h>

extern void *cbzr_go_native_render(uintptr_t handle, int width, int height, size_t *length, double *blockedScroll);
extern int cbzr_go_native_is_webtoon(uintptr_t handle);
extern void cbzr_go_native_scroll(uintptr_t handle, double pixels);
extern void cbzr_go_native_turn(uintptr_t handle, int delta);
extern void cbzr_go_native_event(uintptr_t handle, int event);

static const int CBZREventClear = 1;
static const int CBZREventSave = 2;
static const int CBZREventReturn = 3;
static const int CBZREventToggleWebtoon = 4;
static const int CBZREventRotate = 5;
static const int CBZREventFirst = 6;
static const int CBZREventLast = 7;
static const int CBZREventToggleSpread = 8;
static const int CBZREventZoomIn = 9;
static const int CBZREventZoomOut = 10;
static const int CBZREventResetView = 11;
static const int CBZREventPanUp = 12;
static const int CBZREventPanDown = 13;
static const int CBZREventPanLeft = 14;
static const int CBZREventPanRight = 15;

static void cbzrReleasePixels(void *info, const void *data, size_t size) {
    free((void *)data);
}

@interface CBZRView : NSView <NSWindowDelegate>
@property(nonatomic) uintptr_t handle;
@property(nonatomic) double pendingScroll;
@property(nonatomic) double scrollVelocity;
@property(nonatomic) double pageScroll;
@property(nonatomic) NSInteger inputCount;
@property(nonatomic) BOOL hasInputCount;
@property(nonatomic) BOOL pageGestureConsumed;
@property(nonatomic) BOOL closing;
@property(nonatomic, strong) NSTimer *animationTimer;
@property(nonatomic, strong) NSTimer *pageResetTimer;
- (instancetype)initWithFrame:(NSRect)frame handle:(uintptr_t)handle;
- (void)renderNow;
- (void)queueScroll:(double)pixels;
- (void)cancelScroll;
- (void)discardBlockedScroll:(double)pixels;
- (int)consumeInputCount;
- (void)shutdownWithAction:(int)action;
@end

@implementation CBZRView
- (instancetype)initWithFrame:(NSRect)frame handle:(uintptr_t)handle {
    self = [super initWithFrame:frame];
    if (self) {
        _handle = handle;
    }
    return self;
}

- (BOOL)acceptsFirstResponder {
    return YES;
}

- (BOOL)isFlipped {
    return YES;
}

- (void)drawRect:(NSRect)dirtyRect {
    [super drawRect:dirtyRect];
    if (self.closing) {
        return;
    }
    NSRect bounds = self.bounds;
    CGFloat scale = self.window.backingScaleFactor ?: 1.0;
    int width = MAX(1, (int)llround(NSWidth(bounds) * scale));
    int height = MAX(1, (int)llround(NSHeight(bounds) * scale));
    size_t length = 0;
    double blockedScroll = 0;
    void *pixels = cbzr_go_native_render(self.handle, width, height, &length, &blockedScroll);
    [self discardBlockedScroll:blockedScroll];
    if (!pixels || length < (size_t)width * (size_t)height * 4) {
        [[NSColor blackColor] setFill];
        NSRectFill(bounds);
        free(pixels);
        return;
    }

    CGDataProviderRef provider = CGDataProviderCreateWithData(NULL, pixels, length, cbzrReleasePixels);
    if (!provider) {
        free(pixels);
        return;
    }
    CGColorSpaceRef colorSpace = CGColorSpaceCreateDeviceRGB();
    if (!colorSpace) {
        CGDataProviderRelease(provider);
        return;
    }
    CGImageRef image = CGImageCreate(
        width, height, 8, 32, (size_t)width * 4, colorSpace,
        kCGBitmapByteOrder32Big | kCGImageAlphaPremultipliedLast,
        provider, NULL, false, kCGRenderingIntentDefault
    );
    CGColorSpaceRelease(colorSpace);
    CGDataProviderRelease(provider);

    if (image) {
        NSGraphicsContext *graphics = NSGraphicsContext.currentContext;
        CGContextRef context = graphics.CGContext;
        CGContextSaveGState(context);
        CGContextSetInterpolationQuality(context, kCGInterpolationHigh);
        CGContextTranslateCTM(context, 0, NSHeight(bounds));
        CGContextScaleCTM(context, 1, -1);
        CGContextDrawImage(context, NSMakeRect(0, 0, NSWidth(bounds), NSHeight(bounds)), image);
        CGContextRestoreGState(context);
        CGImageRelease(image);
    }
}

- (void)renderNow {
    if (!self.closing) {
        [self setNeedsDisplay:YES];
    }
}

- (void)keyDown:(NSEvent *)event {
    if (self.closing) {
        return;
    }
    NSString *key = event.charactersIgnoringModifiers;
    if (key.length == 0) {
        return;
    }
    unichar c = [key characterAtIndex:0];
    if ((c >= '1' && c <= '9') || (c == '0' && self.hasInputCount)) {
        NSInteger digit = c - '0';
        self.inputCount = MIN(9999, self.inputCount * 10 + digit);
        self.hasInputCount = YES;
        return;
    }
    switch (c) {
        case 'q':
            [self shutdownWithAction:CBZREventClear];
            return;
        case 'Q':
            [self shutdownWithAction:CBZREventSave];
            return;
        case 'f':
            [self shutdownWithAction:CBZREventReturn];
            return;
        case 's':
            cbzr_go_native_event(self.handle, CBZREventToggleSpread);
            break;
        case '+':
        case '=':
            cbzr_go_native_event(self.handle, CBZREventZoomIn);
            break;
        case '-':
            cbzr_go_native_event(self.handle, CBZREventZoomOut);
            break;
        case '0':
            cbzr_go_native_event(self.handle, CBZREventResetView);
            break;
        case NSUpArrowFunctionKey:
            cbzr_go_native_event(self.handle, CBZREventPanUp);
            break;
        case NSDownArrowFunctionKey:
            cbzr_go_native_event(self.handle, CBZREventPanDown);
            break;
        case NSLeftArrowFunctionKey:
            cbzr_go_native_event(self.handle, CBZREventPanLeft);
            break;
        case NSRightArrowFunctionKey:
            cbzr_go_native_event(self.handle, CBZREventPanRight);
            break;
        case 't':
            [self cancelScroll];
            cbzr_go_native_event(self.handle, CBZREventToggleWebtoon);
            break;
        case 'R':
            [self cancelScroll];
            cbzr_go_native_event(self.handle, CBZREventRotate);
            break;
        case 'g':
            [self cancelScroll];
            cbzr_go_native_event(self.handle, CBZREventFirst);
            break;
        case 'G':
            [self cancelScroll];
            cbzr_go_native_event(self.handle, CBZREventLast);
            break;
        case 'j':
        case 'J':
        case 'k':
        case 'K': {
            int count = [self consumeInputCount];
            if (c == 'k' || c == 'K') count = -count;
            if (cbzr_go_native_is_webtoon(self.handle)) {
                CGFloat scale = self.window.backingScaleFactor ?: 1.0;
                CGFloat fraction = (c == 'J' || c == 'K') ? 0.5 : 1.0;
                [self queueScroll:NSHeight(self.bounds) * scale * fraction * count];
            } else {
                cbzr_go_native_turn(self.handle, count);
            }
            break;
        }
        default:
            [super keyDown:event];
            return;
    }
    [self renderNow];
}

- (void)scrollWheel:(NSEvent *)event {
    if (self.closing) {
        return;
    }
    if (cbzr_go_native_is_webtoon(self.handle)) {
        double delta = event.hasPreciseScrollingDeltas ? event.scrollingDeltaY : event.scrollingDeltaY * 12.0;
        [self queueScroll:-delta];
        return;
    }

    if (!event.hasPreciseScrollingDeltas || (event.phase == NSEventPhaseNone && event.momentumPhase == NSEventPhaseNone)) {
        if (event.scrollingDeltaY != 0) {
            cbzr_go_native_turn(self.handle, event.scrollingDeltaY < 0 ? 1 : -1);
            [self renderNow];
        }
        return;
    }
    if (event.phase == NSEventPhaseBegan) {
        self.pageScroll = 0;
        self.pageGestureConsumed = NO;
    }
    self.pageScroll += event.scrollingDeltaY;
    double threshold = 12.0;
    if (!self.pageGestureConsumed && fabs(self.pageScroll) >= threshold) {
        cbzr_go_native_turn(self.handle, self.pageScroll < 0 ? 1 : -1);
        self.pageGestureConsumed = YES;
        [self renderNow];
    }
    [self.pageResetTimer invalidate];
    CBZRView *viewSelf = self;
    self.pageResetTimer = [NSTimer scheduledTimerWithTimeInterval:0.15 repeats:NO block:^(NSTimer *timer) {
        viewSelf.pageScroll = 0;
        viewSelf.pageGestureConsumed = NO;
        viewSelf.pageResetTimer = nil;
    }];
}

- (int)consumeInputCount {
    int count = self.hasInputCount ? (int)self.inputCount : 1;
    self.inputCount = 0;
    self.hasInputCount = NO;
    return count;
}

- (void)queueScroll:(double)pixels {
    if (self.closing) {
        return;
    }
    if (self.pendingScroll * pixels < 0 || self.scrollVelocity * pixels < 0) {
        [self cancelScroll];
    }
    self.pendingScroll += pixels;
    if (self.animationTimer) {
        return;
    }
    CBZRView *viewSelf = self;
    self.animationTimer = [NSTimer scheduledTimerWithTimeInterval:(1.0 / 120.0) repeats:YES block:^(NSTimer *timer) {
        CBZRView *view = viewSelf;
        if (!view) {
            [timer invalidate];
            return;
        }
        double pending = view.pendingScroll;
        view.scrollVelocity = (view.scrollVelocity + pending * 0.075) * 0.8;
        double step = fmax(-64.0, fmin(64.0, view.scrollVelocity));
        if (fabs(step) > fabs(pending)) {
            step = pending;
        }
        view.pendingScroll -= step;
        cbzr_go_native_scroll(view.handle, step);
        [view renderNow];
        if (fabs(view.pendingScroll) < 0.05 && fabs(view.scrollVelocity) < 0.05) {
            view.pendingScroll = 0;
            view.scrollVelocity = 0;
            [timer invalidate];
            view.animationTimer = nil;
        }
    }];
}

- (void)discardBlockedScroll:(double)pixels {
    if (pixels * self.pendingScroll > 0 || (self.pendingScroll == 0 && pixels * self.scrollVelocity > 0)) {
        [self cancelScroll];
    }
}

- (void)cancelScroll {
    [self.animationTimer invalidate];
    self.animationTimer = nil;
    self.pendingScroll = 0;
    self.scrollVelocity = 0;
}

- (void)shutdownWithAction:(int)action {
    if (self.closing) {
        return;
    }
    self.closing = YES;
    cbzr_go_native_event(self.handle, action);
    [self cancelScroll];
    [self.pageResetTimer invalidate];
    self.pageResetTimer = nil;

    NSWindow *window = self.window;
    window.delegate = nil;
    [window orderOut:nil];
    window.contentView = nil;
    [window close];
    [NSApp stop:nil];
}
- (BOOL)windowShouldClose:(NSWindow *)sender {
    [self shutdownWithAction:CBZREventClear];
    return NO;
}
@end

int cbzr_native_run(uintptr_t handle, const char *title) {
    @autoreleasepool {
        NSRunningApplication *previousApp = NSWorkspace.sharedWorkspace.frontmostApplication;
        NSApplication *app = [NSApplication sharedApplication];
        [app setActivationPolicy:NSApplicationActivationPolicyRegular];

        NSRect frame = NSMakeRect(0, 0, 1100, 800);
        NSWindowStyleMask style = NSWindowStyleMaskTitled |
                                  NSWindowStyleMaskClosable |
                                  NSWindowStyleMaskMiniaturizable |
                                  NSWindowStyleMaskResizable;
        NSWindow *window = [[NSWindow alloc] initWithContentRect:frame
                                                       styleMask:style
                                                         backing:NSBackingStoreBuffered
                                                           defer:NO];
        window.title = [NSString stringWithUTF8String:title];
        window.collectionBehavior = NSWindowCollectionBehaviorFullScreenPrimary;
        window.releasedWhenClosed = NO;
        window.backgroundColor = NSColor.blackColor;
        window.minSize = NSMakeSize(320, 240);

        CBZRView *view = [[CBZRView alloc] initWithFrame:window.contentView.bounds handle:handle];
        view.autoresizingMask = NSViewWidthSizable | NSViewHeightSizable;
        window.contentView = view;

        window.delegate = view;

        [window center];
        [window makeKeyAndOrderFront:nil];
        [window makeFirstResponder:view];
        [app activateIgnoringOtherApps:YES];
        dispatch_async(dispatch_get_main_queue(), ^{
            if (!view.closing && window.isVisible) {
                [window toggleFullScreen:nil];
            }
        });
        [app run];

        [view.animationTimer invalidate];
        [view.pageResetTimer invalidate];
        window.delegate = nil;
        window.contentView = nil;
        [previousApp activateWithOptions:0];
    }
    return 0;
}
