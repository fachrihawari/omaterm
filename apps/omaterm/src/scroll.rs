//! Frame-driven easing for discrete mouse wheels. Precise touchpad deltas
//! remain native; do not apply a second momentum curve to compositor input.

use std::time::Duration;

/// GPUI wheel values are already lines (three per Linux detent).
pub const WHEEL_PIXELS_PER_LINE: f32 = 32.0;

#[derive(Debug)]
pub struct Motion {
    pub position: f32,
    pub target: f32,
}

impl Motion {
    pub fn new(position: f32) -> Self {
        Self {
            position,
            target: position,
        }
    }

    pub fn push(&mut self, delta: f32, min: f32, max: f32) {
        // Reverse immediately instead of paying off the old direction's debt.
        if (self.target - self.position) * delta < 0.0 {
            self.target = self.position;
        }
        self.target = (self.target + delta).clamp(min, max);
    }

    /// Exponential ease-out: independent of refresh rate, monotonic, no
    /// overshoot. New detents extend the target; one frame loop owns all motion.
    pub fn advance(&mut self, elapsed: Duration) -> bool {
        let alpha = 1.0 - (-elapsed.as_secs_f32() / 0.045).exp();
        self.position += (self.target - self.position) * alpha;
        if (self.target - self.position).abs() < 0.5 {
            self.position = self.target;
            false
        } else {
            true
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
        fast.push(-96.0, -1000.0, 0.0);
        slow.push(-96.0, -1000.0, 0.0);
        for _ in 0..12 {
            fast.advance(Duration::from_millis(8));
        }
        for _ in 0..6 {
            slow.advance(Duration::from_millis(16));
        }
        assert!((fast.position - slow.position).abs() < 0.01);
        assert!(fast.position > -96.0 && fast.position < 0.0);
        for _ in 0..100 {
            fast.advance(Duration::from_millis(8));
        }
        assert_eq!(fast.position, -96.0);
    }

    #[test]
    fn reversal_and_bounds_discard_old_debt() {
        let mut motion = Motion::new(-100.0);
        motion.push(-300.0, -200.0, 0.0);
        assert_eq!(motion.target, -200.0);
        motion.push(30.0, -200.0, 0.0);
        assert_eq!(motion.target, -70.0);
        motion.push(1000.0, -200.0, 0.0);
        assert_eq!(motion.target, 0.0);
    }
}
