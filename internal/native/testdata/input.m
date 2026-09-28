#import "../native_darwin.m"
#include <assert.h>

static int turns;
static int webtoon;
static int inverted;
static int linkRequests;
static int linkPage = -1;
static double clickX, clickY;
static void *pendingView;
static uint64_t pendingGeneration;
static int renderRequests, acceptedFrames, shownFrames, releasedFrames;
static void *pendingFrameView;
static uint64_t pendingFrameGeneration;
static int pendingWidth, pendingHeight;
void cbzr_go_native_render_async(uintptr_t handle, int width, int height, void *view, uint64_t generation) {
 assert(pendingFrameView == NULL);
 renderRequests++;
 pendingFrameView = view;
 pendingFrameGeneration = generation;
 pendingWidth = width;
 pendingHeight = height;
}
int cbzr_go_native_accept_frame(uintptr_t frame) { acceptedFrames++; return 1; }
void cbzr_go_native_show_frame(uintptr_t frame) { shownFrames++; }
void cbzr_go_native_release_frame(uintptr_t frame) { releasedFrames++; }
int cbzr_go_native_is_webtoon(uintptr_t handle) { return webtoon; }
void cbzr_go_native_scroll(uintptr_t handle, double pixels) {}
void cbzr_go_native_turn(uintptr_t handle, int delta) { turns += delta; }
void cbzr_go_native_go_to(uintptr_t handle, int page) {
 assert(NSThread.isMainThread);
 linkPage = page;
}
void cbzr_go_native_click(uintptr_t handle, double x, double y, void *view, uint64_t generation) {
 assert(pendingView == NULL);
 linkRequests++;
 clickX = x;
 clickY = y;
 pendingView = view;
 pendingGeneration = generation;
}
void cbzr_go_native_event(uintptr_t handle, int event) {
 if (event == CBZREventToggleWebtoon) webtoon = !webtoon;
 if (event == CBZREventToggleInversion) inverted = !inverted;
}

@interface WheelEvent : NSObject
@end
@implementation WheelEvent
- (BOOL)hasPreciseScrollingDeltas { return NO; }
- (CGFloat)scrollingDeltaY { return -1; }
- (NSEventPhase)phase { return NSEventPhaseNone; }
- (NSEventPhase)momentumPhase { return NSEventPhaseNone; }
@end

static NSEvent *key(NSString *text) {
 return [NSEvent keyEventWithType:NSEventTypeKeyDown location:NSZeroPoint modifierFlags:0 timestamp:0 windowNumber:0 context:nil characters:text charactersIgnoringModifiers:text isARepeat:NO keyCode:0];
}

static NSEvent *mouse(NSEventType type) {
 return [NSEvent mouseEventWithType:type location:NSMakePoint(50, 40) modifierFlags:0 timestamp:0 windowNumber:0 context:nil eventNumber:0 clickCount:1 pressure:1];
}

static void drainMainQueue(void) {
 __block BOOL drained = NO;
 dispatch_async(dispatch_get_main_queue(), ^{ drained = YES; });
 NSDate *deadline = [NSDate dateWithTimeIntervalSinceNow:1];
 while (!drained && deadline.timeIntervalSinceNow > 0) {
  [NSRunLoop.currentRunLoop runMode:NSDefaultRunLoopMode beforeDate:deadline];
 }
 assert(drained);
}

static void completeLink(int page) {
 assert(pendingView != NULL);
 cbzr_native_link_done(pendingView, pendingGeneration, page, NULL);
 pendingView = NULL;
 drainMainQueue();
}

static void completeFrame(void) {
 assert(pendingFrameView != NULL);
 size_t length = (size_t)pendingWidth * pendingHeight * 4;
 cbzr_native_frame_done(pendingFrameView, pendingFrameGeneration, renderRequests,
                        calloc(1, length), pendingWidth, pendingHeight, length, 0);
 pendingFrameView = NULL;
 drainMainQueue();
}

static void paint(CBZRView *view) {
 NSBitmapImageRep *bitmap = [[NSBitmapImageRep alloc] initWithBitmapDataPlanes:NULL
     pixelsWide:100 pixelsHigh:80 bitsPerSample:8 samplesPerPixel:4 hasAlpha:YES
     isPlanar:NO colorSpaceName:NSDeviceRGBColorSpace bytesPerRow:0 bitsPerPixel:0];
 [NSGraphicsContext saveGraphicsState];
 NSGraphicsContext.currentContext = [NSGraphicsContext graphicsContextWithBitmapImageRep:bitmap];
 [view drawRect:view.bounds];
 [NSGraphicsContext restoreGraphicsState];
}

int main(void) {
 @autoreleasepool {
  CBZRView *renderView = [[CBZRView alloc] initWithFrame:NSMakeRect(0, 0, 100, 80) handle:0];
  paint(renderView);
  assert(renderRequests == 1 && renderView.rendering && !renderView.hasFrame);
  [renderView keyDown:key(@"j")];
  [renderView keyDown:key(@"k")];
  paint(renderView);
  assert(renderRequests == 1 && turns == 0);
  completeFrame();
  assert(acceptedFrames == 0 && releasedFrames == 1);
  paint(renderView);
  assert(renderRequests == 2 && renderView.rendering);
  completeFrame();
  assert(acceptedFrames == 1 && shownFrames == 0);
  paint(renderView);
  assert(renderView.hasFrame && shownFrames == 1 && releasedFrames == 2);
  paint(renderView);
  assert(renderRequests == 2 && shownFrames == 1);
  [renderView renderNow];
  paint(renderView);
  assert(renderRequests == 3 && renderView.hasFrame);
  webtoon = 1;
  [renderView queueScroll:800];
  [renderView.animationTimer fire];
  assert(renderView.pendingScroll == 800);
  [renderView cancelScroll];
  webtoon = 0;
  renderView.closing = YES;
  [renderView releaseFrame];
  completeFrame();
  assert(acceptedFrames == 1 && releasedFrames == 3 && renderView.frameImage == NULL);
  CBZRView *view = [[CBZRView alloc] initWithFrame:NSMakeRect(0, 0, 100, 80) handle:0];
  [view mouseDown:mouse(NSEventTypeLeftMouseDown)];
  [view mouseUp:mouse(NSEventTypeLeftMouseUp)];
  assert(linkRequests == 0);
  view.hasFrame = YES;
  [view mouseDown:mouse(NSEventTypeLeftMouseDown)];
  [view mouseUp:mouse(NSEventTypeLeftMouseUp)];
  assert(linkRequests == 1 && fabs(clickX - .5) < 1e-9 && fabs(clickY - .5) < 1e-9);
  completeLink(7);
  assert(linkPage == 7);
  [view mouseDown:mouse(NSEventTypeLeftMouseDown)];
  [view mouseDragged:mouse(NSEventTypeLeftMouseDragged)];
  [view mouseUp:mouse(NSEventTypeLeftMouseUp)];
  assert(linkRequests == 1);
  webtoon = 1;
  [view queueScroll:800];
  [view mouseDown:mouse(NSEventTypeLeftMouseDown)];
  [view mouseUp:mouse(NSEventTypeLeftMouseUp)];
  assert(view.animationTimer != nil);
  completeLink(-1);
  assert(view.animationTimer != nil && view.pendingScroll > 0 && linkPage == 7);
  [view mouseDown:mouse(NSEventTypeLeftMouseDown)];
  [view mouseUp:mouse(NSEventTypeLeftMouseUp)];
  completeLink(7);
  assert(view.animationTimer == nil && view.pendingScroll == 0);
  webtoon = 0;
  [view mouseDown:mouse(NSEventTypeLeftMouseDown)];
  [view mouseUp:mouse(NSEventTypeLeftMouseUp)];
  [view keyDown:key(@"g")];
  completeLink(8);
  assert(linkPage == 7);
  [view mouseDown:mouse(NSEventTypeLeftMouseDown)];
  [view mouseUp:mouse(NSEventTypeLeftMouseUp)];
  [view scrollWheel:(NSEvent *)[WheelEvent new]];
  completeLink(8);
  assert(linkPage == 7);
  turns = 0;
  [view mouseDown:mouse(NSEventTypeLeftMouseDown)];
  [view mouseUp:mouse(NSEventTypeLeftMouseUp)];
  [view setFrameSize:NSMakeSize(200, 160)];
  completeLink(8);
  assert(linkPage == 7 && !view.hasFrame);
  [view setFrameSize:NSMakeSize(100, 80)];
  view.hasFrame = YES;
  [view mouseDown:mouse(NSEventTypeLeftMouseDown)];
  [view mouseUp:mouse(NSEventTypeLeftMouseUp)];
  view.closing = YES;
  completeLink(8);
  assert(linkPage == 7);
  view.closing = NO;
  [view keyDown:key(@"i")];
  assert(inverted == 1);
  [view keyDown:key(@"i")];
  assert(inverted == 0);
  for (NSString *jump in @[@"g", @"G", @"t", @"R"]) {
   webtoon = 1;
   [view queueScroll:800];
   view.scrollVelocity = 20;
   assert(view.animationTimer != nil);
   [view keyDown:key(jump)];
   assert(view.animationTimer == nil && view.pendingScroll == 0 && view.scrollVelocity == 0);
  }
  [view queueScroll:80000];
  view.scrollVelocity = 64;
  [view queueScroll:-1];
  [view queueScroll:-1];
  assert(view.pendingScroll == -2 && view.scrollVelocity == 0);
  [view discardBlockedScroll:64];
  assert(view.pendingScroll == -2 && view.animationTimer != nil);
  [view discardBlockedScroll:-1];
  assert(view.pendingScroll == 0 && view.animationTimer == nil);
  [view queueScroll:80000];
  [view discardBlockedScroll:64];
  assert(view.pendingScroll == 0 && view.animationTimer == nil);
  webtoon = 0;
  for (int i = 0; i < 3; i++) [view scrollWheel:(NSEvent *)[WheelEvent new]];
  assert(turns == 3);
  webtoon = 1;
  [view keyDown:key(@"J")];
  assert(view.pendingScroll == 40);
  [view cancelScroll];
  [view keyDown:key(@"K")];
  assert(view.pendingScroll == -40);
  [view cancelScroll];
 }
 return 0;
}
