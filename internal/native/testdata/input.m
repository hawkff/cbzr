#import "../native_darwin.m"
#include <assert.h>

static int turns;
static int webtoon;
void *cbzr_go_native_render(uintptr_t handle, int width, int height, size_t *length, double *blockedScroll) { *length = 0; *blockedScroll = 0; return NULL; }
int cbzr_go_native_is_webtoon(uintptr_t handle) { return webtoon; }
void cbzr_go_native_scroll(uintptr_t handle, double pixels) {}
void cbzr_go_native_turn(uintptr_t handle, int delta) { turns += delta; }
void cbzr_go_native_event(uintptr_t handle, int event) { if (event == CBZREventToggleWebtoon) webtoon = !webtoon; }

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

int main(void) {
 @autoreleasepool {
  CBZRView *view = [[CBZRView alloc] initWithFrame:NSMakeRect(0, 0, 100, 80) handle:0];
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
