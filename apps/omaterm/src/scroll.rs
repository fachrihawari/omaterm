//! Frame-driven scrolling for discrete wheels and precise touchpads.

use std::time::Duration;

/// GPUI wheel values are already lines (three per Linux detent).
pub const WHEEL_PIXELS_PER_LINE: f32 = 32.0;
/// Compensates for GPUI's conservative Wayland pixel-axis scale without
/// changing the velocity curve or coast duration.
pub const TOUCHPAD_PIXELS_PER_PIXEL: f32 = 1.25;
const TOUCHPAD_RELEASE_DELAY: Duration = Duration::from_millis(42);
const TOUCHPAD_DECELERATION: f32 = 0.22;
const MIN_TOUCHPAD_VELOCITY: f32 = 8.0;

#[derive(Debug)]
enum Source {
    Wheel,
    Touchpad { velocity: f32 },
}

#[derive(Debug)]
pub struct Motion {
    pub position: f32,
    pub target: f32,
    source: Source,
    /// When true, scroll jumps straight to the target instead of easing or
    /// coasting. Set from the `appearance.reduced-motion` config key, which is
    /// the app's equivalent of the desktop `prefers-reduced-motion` hint: some
    /// users are made motion-sick by animation, and a terminal is not worth it.
    reduced: bool,
}

impl Motion {
    pub fn new(position: f32) -> Self {
        Self {
            position,
            target: position,
            source: Source::Wheel,
            reduced: false,
        }
    }

    /// Opt into reduced motion: every push lands on its target immediately.
    pub fn with_reduced_motion(mut self, reduced: bool) -> Self {
        self.reduced = reduced;
        self
    }

    pub fn push_wheel(&mut self, delta: f32, min: f32, max: f32) {
        // Reverse immediately instead of paying off the old direction's debt.
        if (self.target - self.position) * delta < 0.0 {
            self.target = self.position;
        }
        self.target = (self.target + delta).clamp(min, max);
        self.source = Source::Wheel;
        if self.reduced {
            // No ease-out: land on the target this frame.
            self.position = self.target;
        }
    }

    /// Applies precise input immediately and records its release velocity. A
    /// new direction replaces stale momentum instead of fighting it.
    pub fn push_touchpad(&mut self, delta: f32, elapsed: Option<Duration>, min: f32, max: f32) {
        let velocity = elapsed.map_or(0.0, |elapsed| delta / elapsed.as_secs_f32().max(0.001));
        let position = (self.position + delta).clamp(min, max);
        let hit_boundary = position != self.position + delta;
        self.position = position;
        self.target = position;
        self.source = Source::Touchpad {
            // Reduced motion also drops the coast: momentum that keeps moving
            // after the fingers stop is the worst offender for motion sickness.
            velocity: if hit_boundary || self.reduced {
                0.0
            } else {
                velocity
            },
        };
    }

    /// Advances a discrete-wheel ease-out or a velocity-based touchpad coast.
    /// Wayland's GPUI backend does not expose an axis-stop event, so the caller
    /// supplies elapsed time since the last precise input to infer release.
    pub fn advance(&mut self, elapsed: Duration, input_idle: Duration, min: f32, max: f32) -> bool {
        match &mut self.source {
            Source::Wheel => {
                let alpha = 1.0 - (-elapsed.as_secs_f32() / 0.045).exp();
                self.position += (self.target - self.position) * alpha;
                if (self.target - self.position).abs() < 0.5 {
                    self.position = self.target;
                    false
                } else {
                    true
                }
            }
            Source::Touchpad { velocity } => {
                if input_idle < TOUCHPAD_RELEASE_DELAY {
                    return true;
                }
                let seconds = elapsed.as_secs_f32();
                let decay = (-seconds / TOUCHPAD_DECELERATION).exp();
                let distance = *velocity * TOUCHPAD_DECELERATION * (1.0 - decay);
                let position = (self.position + distance).clamp(min, max);
                let hit_boundary = position != self.position + distance;
                self.position = position;
                self.target = position;
                *velocity *= decay;
                !hit_boundary && velocity.abs() >= MIN_TOUCHPAD_VELOCITY
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn easing_is_refresh_rate_independent_and_settles_exactly() {
        let mut fast = Motion::new(0.0);
        let mut slow = Motion::new(0.0);
        fast.push_wheel(-96.0, -1000.0, 0.0);
        slow.push_wheel(-96.0, -1000.0, 0.0);
        for _ in 0..12 {
            fast.advance(Duration::from_millis(8), Duration::ZERO, -1000.0, 0.0);
        }
        for _ in 0..6 {
            slow.advance(Duration::from_millis(16), Duration::ZERO, -1000.0, 0.0);
        }
        assert!((fast.position - slow.position).abs() < 0.01);
        assert!(fast.position > -96.0 && fast.position < 0.0);
        for _ in 0..100 {
            fast.advance(Duration::from_millis(8), Duration::ZERO, -1000.0, 0.0);
        }
        assert_eq!(fast.position, -96.0);
    }

    #[test]
    fn reversal_and_bounds_discard_old_debt() {
        let mut motion = Motion::new(-100.0);
        motion.push_wheel(-300.0, -200.0, 0.0);
        assert_eq!(motion.target, -200.0);
        motion.push_wheel(30.0, -200.0, 0.0);
        assert_eq!(motion.target, -70.0);
        motion.push_wheel(1000.0, -200.0, 0.0);
        assert_eq!(motion.target, 0.0);
    }

    /// Reduced motion lands on the target immediately: no ease-out frame is
    /// needed, so the caller's `advance` sees no work to do.
    #[test]
    fn reduced_motion_snaps_wheel_input_to_the_target() {
        let mut motion = Motion::new(0.0).with_reduced_motion(true);
        motion.push_wheel(-96.0, -1000.0, 0.0);
        assert_eq!(motion.position, -96.0, "wheel input must land immediately");
        assert_eq!(motion.target, -96.0);
        assert!(
            !motion.advance(Duration::from_millis(8), Duration::ZERO, -1000.0, 0.0),
            "nothing left to animate"
        );
    }

    /// Reduced motion also drops touchpad momentum: the coast is the part most
    /// likely to bother motion-sensitive users.
    #[test]
    fn reduced_motion_drops_touchpad_coast() {
        let mut motion = Motion::new(0.0).with_reduced_motion(true);
        motion.push_touchpad(40.0, Some(Duration::from_millis(10)), 0.0, 1000.0);
        assert_eq!(motion.position, 40.0);
        // No velocity means the coast branch settles instead of continuing.
        assert!(!motion.advance(
            Duration::from_millis(16),
            Duration::from_millis(100),
            0.0,
            1000.0
        ));
        assert_eq!(motion.position, 40.0);
    }

    /// Full motion is the default, so the setting is opt-in.
    #[test]
    fn motion_defaults_to_easing() {
        let mut motion = Motion::new(0.0);
        motion.push_wheel(-96.0, -1000.0, 0.0);
        assert_eq!(motion.position, 0.0, "default motion starts at rest");
        assert!(motion.advance(Duration::from_millis(8), Duration::ZERO, -1000.0, 0.0));
    }

    #[test]
    fn a_fast_touchpad_flick_coasts_farther_than_a_slow_drag() {
        let mut slow = Motion::new(0.0);
        let mut fast = Motion::new(0.0);
        slow.push_touchpad(12.0, Some(Duration::from_millis(60)), 0.0, 1000.0);
        fast.push_touchpad(12.0, Some(Duration::from_millis(6)), 0.0, 1000.0);

        for _ in 0..120 {
            slow.advance(
                Duration::from_millis(8),
                Duration::from_millis(50),
                0.0,
                1000.0,
            );
            fast.advance(
                Duration::from_millis(8),
                Duration::from_millis(50),
                0.0,
                1000.0,
            );
        }

        assert!(slow.position > 12.0);
        assert!(fast.position > slow.position * 5.0);
    }

    #[test]
    fn touchpad_stops_at_bounds_without_debt() {
        let mut motion = Motion::new(90.0);
        motion.push_touchpad(20.0, Some(Duration::from_millis(10)), 0.0, 100.0);
        assert_eq!(motion.position, 100.0);
        assert!(!motion.advance(
            Duration::from_millis(16),
            Duration::from_millis(50),
            0.0,
            100.0,
        ));
    }

    #[test]
    fn first_touchpad_event_does_not_invent_momentum() {
        let mut motion = Motion::new(0.0);
        motion.push_touchpad(12.0, None, 0.0, 1000.0);
        assert!(!motion.advance(
            Duration::from_millis(16),
            Duration::from_millis(50),
            0.0,
            1000.0,
        ));
        assert_eq!(motion.position, 12.0);
    }
}
