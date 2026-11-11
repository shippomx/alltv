use std::{
    collections::BTreeMap,
    time::{Duration, Instant},
};

pub struct RequestWindow {
    next: u32,
    limit: usize,
    retry_after: Duration,
    outstanding: BTreeMap<u32, Instant>,
}

impl RequestWindow {
    pub fn new(next: u32, limit: usize, retry_after: Duration) -> Self {
        Self {
            next,
            limit,
            retry_after,
            outstanding: BTreeMap::new(),
        }
    }

    pub fn fill(&mut self, now: Instant) -> Vec<u32> {
        let mut sequences = Vec::with_capacity(self.limit.saturating_sub(self.outstanding.len()));
        while self.outstanding.len() < self.limit {
            let sequence = self.next;
            self.next = self.next.wrapping_add(1);
            self.outstanding.insert(sequence, now);
            sequences.push(sequence);
        }
        sequences
    }

    pub fn received(&mut self, sequence: u32) -> bool {
        self.outstanding.remove(&sequence).is_some()
    }

    pub fn advance(&mut self, next: u32) {
        self.outstanding.retain(|sequence, _| *sequence >= next);
    }

    pub fn retries(&mut self, now: Instant) -> Vec<u32> {
        let mut sequences = Vec::new();
        for (sequence, sent) in &mut self.outstanding {
            if now.duration_since(*sent) >= self.retry_after {
                *sent = now;
                sequences.push(*sequence);
            }
        }
        sequences
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn fills_acks_advances_and_retries() {
        let start = Instant::now();
        let mut window = RequestWindow::new(5, 3, Duration::from_millis(250));
        assert_eq!(window.fill(start), vec![5, 6, 7]);
        assert!(window.received(6));
        assert_eq!(window.fill(start), vec![8]);
        window.advance(7);
        assert_eq!(window.retries(start + Duration::from_millis(249)), vec![]);
        assert_eq!(
            window.retries(start + Duration::from_millis(250)),
            vec![7, 8]
        );
    }
}
